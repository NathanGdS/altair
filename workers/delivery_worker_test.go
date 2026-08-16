package workers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
