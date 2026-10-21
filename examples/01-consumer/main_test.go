package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewMux(t *testing.T) {
	t.Run("webhook increments the received counter and stats reports it", func(t *testing.T) {
		// Arrange
		mux, received := newMux()

		// Act
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhook", nil))
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/webhook", nil))

		statsResponse := httptest.NewRecorder()
		mux.ServeHTTP(statsResponse, httptest.NewRequest(http.MethodGet, "/stats", nil))

		// Assert
		assert.Equal(t, int64(2), received.Load())
		assert.JSONEq(t, `{"received": 2}`, statsResponse.Body.String())
	})
}

func TestBuildWebhookURL(t *testing.T) {
	t.Run("defaults to localhost for host runs", func(t *testing.T) {
		// Arrange
		host, port := "localhost", "9090"

		// Act
		url := buildWebhookURL(host, port)

		// Assert
		assert.Equal(t, "http://localhost:9090/webhook", url)
	})

	t.Run("uses the given host so altair can reach the consumer across containers", func(t *testing.T) {
		// Arrange
		host, port := "consumer", "9090"

		// Act
		url := buildWebhookURL(host, port)

		// Assert
		assert.Equal(t, "http://consumer:9090/webhook", url)
	})
}
