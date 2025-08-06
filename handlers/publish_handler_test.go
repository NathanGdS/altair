package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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
