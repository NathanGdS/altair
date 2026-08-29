package workers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDeliverPending_SurvivesLineLargerThanDefaultScannerBuffer is a regression test for C1:
// bufio.Scanner's default MaxScanTokenSize is 64KB, and a delivery line JSON-escapes the
// whole message payload, which can push a single line past that limit for legitimate
// /publish input well under any existing size limit. Before the fix, that produced
// "bufio.Scanner: token too long" from scanner.Err(), and every line already parsed in that
// file — including short, perfectly valid lines sharing the file with the oversized one —
// was discarded by the caller with the underlying file already renamed away.
//
// This test builds a delivery line whose payload alone exceeds 64KB (comfortably larger
// than bufio.Scanner's default token limit, but well within the widened buffer), appends it
// to the same pending file as an ordinary short line, and asserts both are still delivered.
// It would fail with "token too long" (and both messages lost) without the
// scanner.Buffer(...) fix in readAndTruncatePendingLines.
func TestDeliverPending_SurvivesLineLargerThanDefaultScannerBuffer(t *testing.T) {
	t.Run("delivers both a normal line and a line whose payload exceeds the scanner's default 64KB limit", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDeliveryDirectories()

		var mu sync.Mutex
		receivedBodies := map[string]int{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			receivedBodies[string(body)]++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		path := filepath.Join(DeliveryPendingDir, "orders-2026-08-16.json")

		// A short, ordinary line. If the oversized line below poisons the whole scan, this
		// one gets silently discarded too, even though it's individually well-formed and
		// tiny — that's exactly the "0 lines survive, not 1" failure mode the report
		// verified empirically.
		assert.NoError(t, appendDeliveryLine(path, deliveryLine{
			MessageID:  "short-1",
			Origin:     "orders",
			ConsumerID: "c1",
			WebhookURL: server.URL,
			Payload:    `{"id":"short-1"}`,
		}))

		// A line whose marshaled JSON comfortably exceeds bufio.Scanner's default 64KB
		// MaxScanTokenSize, but stays well inside the widened 8MB buffer.
		largePayload := `{"id":"large-1","blob":"` + strings.Repeat("x", 100*1024) + `"}`
		assert.Greater(t, len(largePayload), 64*1024, "payload must exceed the scanner's default token limit to exercise the fix")
		assert.NoError(t, appendDeliveryLine(path, deliveryLine{
			MessageID:  "large-1",
			Origin:     "orders",
			ConsumerID: "c1",
			WebhookURL: server.URL,
			Payload:    largePayload,
		}))

		// Act
		deliverPending()

		// Assert: both lines were delivered, and the pending file was fully drained with
		// nothing left behind in deliveries/failed.
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, 1, receivedBodies[`{"id":"short-1"}`], "the short line sharing the file with the oversized one should still be delivered")
		assert.Equal(t, 1, receivedBodies[largePayload], "the oversized line itself should still be delivered")
		assert.Len(t, receivedBodies, 2, "no extra or missing deliveries")

		failedFiles, err := os.ReadDir(DeliveryFailedDir)
		assert.NoError(t, err)
		assert.Empty(t, failedFiles)
	})
}

func TestDeliverPending(t *testing.T) {
	t.Run("delivers a pending message successfully and empties the pending file", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDeliveryDirectories()

		var receivedBody string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			receivedBody = string(body)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		path := filepath.Join(DeliveryPendingDir, "orders-2026-08-16.json")
		assert.NoError(t, appendDeliveryLine(path, deliveryLine{
			MessageID:  "msg-1",
			Origin:     "orders",
			ConsumerID: "c1",
			WebhookURL: server.URL,
			Payload:    `{"id":"msg-1"}`,
		}))

		// Act
		deliverPending()

		// Assert
		assert.Equal(t, `{"id":"msg-1"}`, receivedBody)

		// readAndTruncatePendingLines removes the pending file entirely after a successful
		// drain (rename-then-process), rather than truncating it to zero bytes in place, so
		// the pending directory should be empty here — not merely "every file present is
		// empty" (which would be vacuously true if the directory were empty for a different
		// reason too).
		pendingFiles, err := os.ReadDir(DeliveryPendingDir)
		assert.NoError(t, err)
		assert.Empty(t, pendingFiles)

		failedFiles, err := os.ReadDir(DeliveryFailedDir)
		assert.NoError(t, err)
		assert.Empty(t, failedFiles)
	})

	t.Run("retries on failure and writes to the failed file after exhausting retries", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDeliveryDirectories()

		attempts := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		path := filepath.Join(DeliveryPendingDir, "orders-2026-08-16.json")
		assert.NoError(t, appendDeliveryLine(path, deliveryLine{
			MessageID:  "msg-2",
			Origin:     "orders",
			ConsumerID: "c1",
			WebhookURL: server.URL,
			Payload:    `{"id":"msg-2"}`,
		}))

		// Act
		deliverPending()

		// Assert
		assert.Equal(t, 3, attempts)

		failedFiles, err := os.ReadDir(DeliveryFailedDir)
		assert.NoError(t, err)
		assert.Len(t, failedFiles, 1)

		content, err := os.ReadFile(filepath.Join(DeliveryFailedDir, failedFiles[0].Name()))
		assert.NoError(t, err)

		var failed deliveryLine
		assert.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(content))), &failed))
		assert.Equal(t, "msg-2", failed.MessageID)
	})
}

// TestDeliverPending_RecoversOrphanedReadingFileAfterCrash is a regression test for a
// crash-recovery data-loss path the unique-per-call reading path (readingPath :=
// fmt.Sprintf("%s.reading.%d", path, time.Now().UnixNano())) fixes.
//
// Scenario: a forceful kill lands in the window after readAndTruncatePendingLines renames
// path -> path+".reading" but before it removes that file. That orphan sits on disk with
// unread lines. The process restarts, fresh messages accumulate at path again (via O_CREATE).
// On the next drain, os.ReadDir returns both the fresh path and the orphan; with the old fixed
// ".reading" suffix, the fresh file's own rename target would collide with the orphan's name,
// and os.Rename's replace-existing semantics would silently overwrite (destroy) the orphan
// before anything ever read it — no trace in deliveries/failed. With a unique per-call
// timestamped suffix, the orphan is just another independent directory entry that deliverPending
// picks up and delivers on its own, recovered late instead of lost.
//
// This test simulates that exact post-crash disk state directly (a fresh pending file plus a
// manually-created orphan with the old fixed-suffix naming pattern) and asserts deliverPending
// delivers BOTH sets of lines — nothing lost, nothing overwritten.
func TestDeliverPending_RecoversOrphanedReadingFileAfterCrash(t *testing.T) {
	t.Run("delivers both the fresh pending file and a crash-orphaned .reading file", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDeliveryDirectories()

		var mu sync.Mutex
		receivedIDs := map[string]int{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// deliverOne POSTs deliveryLine.Payload as the raw body, so the payload string
			// itself (set per-line below) is what identifies which message arrived.
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			receivedIDs[string(body)]++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		path := filepath.Join(DeliveryPendingDir, "orders-2026-08-16.json")

		// Fresh file: what accumulated at the original path after the process restarted.
		assert.NoError(t, appendDeliveryLine(path, deliveryLine{
			MessageID:  "fresh-1",
			Origin:     "orders",
			ConsumerID: "c1",
			WebhookURL: server.URL,
			Payload:    `{"id":"fresh-1"}`,
		}))

		// Orphan: simulates a prior crash that renamed path -> path+".reading" but was killed
		// before removing it. Written directly (not via appendDeliveryLine, which would target
		// "path", not the orphan's name) using the OLD fixed-suffix naming pattern this fix
		// replaces, since that's exactly the on-disk artifact a pre-fix crash would leave.
		orphanPath := path + ".reading"
		orphanLine, err := json.Marshal(deliveryLine{
			MessageID:  "orphan-1",
			Origin:     "orders",
			ConsumerID: "c1",
			WebhookURL: server.URL,
			Payload:    `{"id":"orphan-1"}`,
		})
		assert.NoError(t, err)
		assert.NoError(t, os.WriteFile(orphanPath, append(orphanLine, '\n'), 0644))

		// Act
		deliverPending()

		// Assert: both the fresh message and the orphan's message were delivered exactly once —
		// the orphan was neither silently overwritten nor left undelivered.
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, 1, receivedIDs[`{"id":"fresh-1"}`], "fresh pending file's message should be delivered exactly once")
		assert.Equal(t, 1, receivedIDs[`{"id":"orphan-1"}`], "crash-orphaned message should be recovered and delivered exactly once")
		assert.Len(t, receivedIDs, 2, "no extra or missing deliveries")

		failedFiles, err := os.ReadDir(DeliveryFailedDir)
		assert.NoError(t, err)
		assert.Empty(t, failedFiles)
	})
}

// TestReadAndTruncatePendingLines_ConcurrentAppendsNotLost is a regression test for two races
// this file's fixes closed, both guarded by the same deliveryFileMu:
//
//  1. Writer-vs-renamer (rename-then-process fix): readAndTruncatePendingLines used to open the
//     pending file, scan it to EOF, close it, and only then call os.Truncate(path, 0). Any line
//     appended by a concurrent writer after the scan reached EOF but before the truncate
//     executed was silently wiped.
//  2. Writer-vs-writer (deliveryFileMu fix): concurrent appendDeliveryLine calls to the same
//     path used to race each other directly — on Windows this reproducibly hit
//     ERROR_SHARING_VIOLATION on os.OpenFile, and enqueueDeliveries drops a line on write
//     failure with no retry, permanently losing it.
//
// This can't be turned into a fully deterministic unit test (it depends on exact goroutine
// scheduling), so instead it reproduces the actual production shape of both races at once:
// DeliveryWorker calls readAndTruncatePendingLines exactly once per poll tick (never in a tight
// loop), while up to shared.ConsumerWorkingPool goroutines from ConsumerWorker's pool can call
// enqueueDeliveries -> appendDeliveryLine concurrently for messages sharing an origin (exactly
// the example consumer's load-test shape: every message uses the same origin, hence the same
// pending file). Each trial below fires a batch of concurrent appenders racing a single
// concurrent drain call, repeated across many trials to build statistical confidence. It
// asserts every appended line is eventually observed exactly once — none lost, none duplicated.
func TestReadAndTruncatePendingLines_ConcurrentAppendsNotLost(t *testing.T) {
	t.Run("no delivery line is lost when a periodic drain races a burst of concurrent appends", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDeliveryDirectories()

		path := filepath.Join(DeliveryPendingDir, "race-2026-08-16.json")
		const trials = 200
		const appendersPerTrial = 24

		var mu sync.Mutex
		seen := make(map[string]int)
		nextID := 0

		drain := func() {
			lines, err := readAndTruncatePendingLines(path)
			assert.NoError(t, err)
			for _, l := range lines {
				seen[l.MessageID]++
			}
		}

		// Act: for each trial, fire a batch of concurrent appenders (mirroring
		// enqueueDeliveries broadcasting one message to many consumers, or many pool
		// goroutines processing same-origin messages concurrently) and race a single
		// drain call against them (mirroring DeliveryWorker's one-drain-per-tick loop).
		for trial := 0; trial < trials; trial++ {
			var appendWG sync.WaitGroup
			for i := 0; i < appendersPerTrial; i++ {
				mu.Lock()
				nextID++
				msgID := strconv.Itoa(nextID)
				mu.Unlock()

				appendWG.Add(1)
				go func(id string) {
					defer appendWG.Done()
					assert.NoError(t, appendDeliveryLine(path, deliveryLine{MessageID: id}))
				}(msgID)
			}

			mu.Lock()
			drain()
			mu.Unlock()

			appendWG.Wait()
		}

		// Final drain to collect anything left over from the last trial.
		mu.Lock()
		drain()

		// Assert: every line appended across every trial was seen exactly once.
		assert.Len(t, seen, nextID, "expected every appended line to be observed")
		for i := 1; i <= nextID; i++ {
			assert.Equal(t, 1, seen[strconv.Itoa(i)], "message %d should be observed exactly once", i)
		}
		mu.Unlock()
	})
}
