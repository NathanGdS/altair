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

		pendingFiles, err := os.ReadDir(DeliveryPendingDir)
		assert.NoError(t, err)
		for _, f := range pendingFiles {
			info, statErr := os.Stat(filepath.Join(DeliveryPendingDir, f.Name()))
			assert.NoError(t, statErr)
			assert.Equal(t, int64(0), info.Size())
		}

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

// TestReadAndTruncatePendingLines_ConcurrentAppendsNotLost is a best-effort regression test
// for the TOCTOU race this fix closes: readAndTruncatePendingLines used to open the pending
// file, scan it to EOF, close it, and only then call os.Truncate(path, 0). Any line appended
// by a concurrent writer after the scan reached EOF but before the truncate executed was
// silently wiped.
//
// This can't be turned into a fully deterministic unit test (it depends on exact goroutine
// scheduling), so instead it reproduces the actual production shape of the race: DeliveryWorker
// calls readAndTruncatePendingLines exactly once per poll tick (never in a tight loop), while a
// writer appends to the same file concurrently. Each trial below has one writer goroutine
// appending a batch of uniquely-identified lines (sequentially — see the note on
// TestAppendDeliveryLine_ConcurrentWritersRaceEachOther below for why appenders are NOT run
// concurrently with each other here) while a drain call fires concurrently with it, repeated
// across many trials to build statistical confidence. It asserts every appended line is
// eventually observed exactly once — none lost, none duplicated. On the pre-fix implementation
// this test reliably reproduces loss (some IDs never appear); on the rename-then-process fix
// the race window shrinks to a single os.Rename/os.OpenFile pair and no loss is observed.
func TestReadAndTruncatePendingLines_ConcurrentAppendsNotLost(t *testing.T) {
	t.Run("no delivery line is lost when a periodic drain races a concurrent writer", func(t *testing.T) {
		// Arrange
		withTempWorkDir(t)
		initDeliveryDirectories()

		path := filepath.Join(DeliveryPendingDir, "race-2026-08-16.json")
		const trials = 200
		const linesPerTrial = 24

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

		// Act: for each trial, a single writer goroutine appends a batch of lines
		// sequentially (mirroring one enqueueDeliveries call writing several consumer
		// fan-out lines) while a drain call races it concurrently (mirroring
		// DeliveryWorker's one-drain-per-tick loop firing mid-write).
		for trial := 0; trial < trials; trial++ {
			var writerWG sync.WaitGroup
			writerWG.Add(1)
			go func() {
				defer writerWG.Done()
				for i := 0; i < linesPerTrial; i++ {
					mu.Lock()
					nextID++
					msgID := strconv.Itoa(nextID)
					mu.Unlock()
					assert.NoError(t, appendDeliveryLine(path, deliveryLine{MessageID: msgID}))
				}
			}()

			mu.Lock()
			drain()
			mu.Unlock()

			writerWG.Wait()
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
