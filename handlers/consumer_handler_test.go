package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nathangds/altair/shared"
)

func TestConsumerHandlers(t *testing.T) {
	t.Run("register returns 200 and an id", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		body := `{"origin": "orders", "webhook_url": "https://example.com/hook"}`
		request := httptest.NewRequest(http.MethodPost, "/consumers", bytes.NewBufferString(body))
		response := httptest.NewRecorder()

		// Act
		RegisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusOK, response.Code)
		var resp struct {
			ID string `json:"id"`
		}
		err = json.Unmarshal(response.Body.Bytes(), &resp)
		assert.NoError(t, err)
		assert.NotEmpty(t, resp.ID)
	})

	t.Run("register returns 400 when origin is missing", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		body := `{"webhook_url": "https://example.com/hook"}`
		request := httptest.NewRequest(http.MethodPost, "/consumers", bytes.NewBufferString(body))
		response := httptest.NewRecorder()

		// Act
		RegisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})

	t.Run("heartbeat returns 200 for a registered consumer", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		consumer, err := shared.Consumers.Register("orders", "https://example.com/hook")
		assert.NoError(t, err)

		request := httptest.NewRequest(http.MethodPost, "/consumers/"+consumer.ID+"/heartbeat", nil)
		request.SetPathValue("id", consumer.ID)
		response := httptest.NewRecorder()

		// Act
		ConsumerHeartbeatHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusOK, response.Code)
	})

	t.Run("heartbeat returns 404 for unknown consumer", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		request := httptest.NewRequest(http.MethodPost, "/consumers/unknown/heartbeat", nil)
		request.SetPathValue("id", "unknown")
		response := httptest.NewRecorder()

		// Act
		ConsumerHeartbeatHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusNotFound, response.Code)
	})

	t.Run("unregister deactivates the consumer", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		consumer, err := shared.Consumers.Register("orders", "https://example.com/hook")
		assert.NoError(t, err)

		request := httptest.NewRequest(http.MethodDelete, "/consumers/"+consumer.ID, nil)
		request.SetPathValue("id", consumer.ID)
		response := httptest.NewRecorder()

		// Act
		UnregisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Empty(t, shared.Consumers.ActiveConsumersForOrigin("orders"))
	})
}
