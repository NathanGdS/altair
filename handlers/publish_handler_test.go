package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nathangds/altair/shared"
)

func TestPublishHandler(t *testing.T) {

	t.Run("should publish a message and return 200", func(t *testing.T) {
		// Arrange
		jsonBody := `{"data": {"key": "value"}}`
		request, err := http.NewRequest(http.MethodPost, "/publish", bytes.NewBufferString(jsonBody))
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

		// Act
		response := httptest.NewRecorder()

		// Assert
		PublishHandler(response, request)

		if response.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, response.Code)
		}

		var responseMessage Message
		err = json.Unmarshal(response.Body.Bytes(), &responseMessage)
		assert.NoError(t, err)
		assert.NotEmpty(t, responseMessage.Id)
		assert.NotEmpty(t, responseMessage.ReceivedAt)
	})
}

// TestPublishHandler_OriginValidation is an I3 regression test: origin flows from /publish
// downstream into workers/delivery.go's pendingFilePath/failedFilePath, which interpolate it
// directly into a filesystem path. A path-traversal origin (e.g. "../../evil") must be
// rejected at the /publish entry point rather than reaching those sinks.
//
// Kept separate from TestPublishHandler above (which exercises the no-origin/non-stress-test
// case and has a pre-existing, unrelated failure) so these assertions aren't entangled with
// that known issue.
func TestPublishHandler_OriginValidation(t *testing.T) {
	t.Run("accepts a well-formed origin", func(t *testing.T) {
		// Arrange
		jsonBody := `{"origin": "orders", "data": {"key": "value"}}`
		request, err := http.NewRequest(http.MethodPost, "/publish", bytes.NewBufferString(jsonBody))
		assert.NoError(t, err)
		response := httptest.NewRecorder()

		// Act
		PublishHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusOK, response.Code)
	})

	t.Run("rejects an origin containing path-traversal characters", func(t *testing.T) {
		// Arrange
		jsonBody := `{"origin": "../../evil", "data": {"key": "value"}}`
		request, err := http.NewRequest(http.MethodPost, "/publish", bytes.NewBufferString(jsonBody))
		assert.NoError(t, err)
		response := httptest.NewRecorder()

		// Act
		PublishHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})
}

func TestPublishHandler_Metrics(t *testing.T) {
	t.Run("increments the published counter on an accepted message", func(t *testing.T) {
		// Arrange
		request, err := http.NewRequest(http.MethodPost, "/publish", bytes.NewBufferString(`{"origin": "orders", "data": {}}`))
		assert.NoError(t, err)
		response := httptest.NewRecorder()
		before := shared.PublishedTotal.Load()

		// Act
		PublishHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, before+1, shared.PublishedTotal.Load())
	})

	t.Run("does not count a rejected message", func(t *testing.T) {
		// Arrange
		request, err := http.NewRequest(http.MethodPost, "/publish", bytes.NewBufferString(`{"origin": "../evil", "data": {}}`))
		assert.NoError(t, err)
		response := httptest.NewRecorder()
		before := shared.PublishedTotal.Load()

		// Act
		PublishHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Equal(t, before, shared.PublishedTotal.Load())
	})
}

func TestWriterQueueDepth(t *testing.T) {
	t.Run("reports the writer buffer capacity", func(t *testing.T) {
		// Act
		length, capacity := WriterQueueDepth()

		// Assert
		assert.Equal(t, 200000, capacity)
		assert.GreaterOrEqual(t, length, 0)
	})
}
