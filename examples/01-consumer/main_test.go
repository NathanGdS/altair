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
