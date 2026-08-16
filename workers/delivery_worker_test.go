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
