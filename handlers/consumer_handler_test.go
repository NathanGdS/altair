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

	// I3 regression: origin is interpolated directly into a filesystem path in
	// workers/delivery.go's pendingFilePath/failedFilePath. POST /consumers is unauthenticated,
	// so a path-traversal origin must be rejected here rather than reaching those sinks.
	t.Run("register returns 400 when origin contains path-traversal characters", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		body := `{"origin": "../../evil", "webhook_url": "https://example.com/hook"}`
		request := httptest.NewRequest(http.MethodPost, "/consumers", bytes.NewBufferString(body))
		response := httptest.NewRecorder()

		// Act
		RegisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusBadRequest, response.Code)
		assert.Empty(t, shared.Consumers.ActiveConsumersForOrigin("../../evil"))
	})

	// I2 regression: POST /consumers is unauthenticated, so webhook_url must be validated as a
	// structurally sane http(s) URL. This closes an SSRF-adjacent gap (arbitrary URL schemes)
	// and a resource-exhaustion gap (a structurally invalid URL previously burned all delivery
	// retries + backoff forever, since nothing ever rejected it).
	t.Run("register returns 400 when webhook_url is missing a scheme", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		body := `{"origin": "orders", "webhook_url": "example.com/hook"}`
		request := httptest.NewRequest(http.MethodPost, "/consumers", bytes.NewBufferString(body))
		response := httptest.NewRecorder()

		// Act
		RegisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})

	t.Run("register returns 400 when webhook_url uses a non-http(s) scheme", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		body := `{"origin": "orders", "webhook_url": "ftp://example.com/hook"}`
		request := httptest.NewRequest(http.MethodPost, "/consumers", bytes.NewBufferString(body))
		response := httptest.NewRecorder()

		// Act
		RegisterConsumerHandler(response, request)

		// Assert
		assert.Equal(t, http.StatusBadRequest, response.Code)
	})

	t.Run("register returns 400 when webhook_url has no host", func(t *testing.T) {
		// Arrange
		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		body := `{"origin": "orders", "webhook_url": "http:///hook"}`
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
