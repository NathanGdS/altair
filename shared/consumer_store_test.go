package shared

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConsumerStore_RegisterAndActiveConsumersForOrigin(t *testing.T) {
	t.Run("should register a consumer and make it visible via the cache", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		// Act
		consumer, err := store.Register("orders", "https://example.com/hook")

		// Assert
		assert.NoError(t, err)
		assert.NotEmpty(t, consumer.ID)
		assert.Equal(t, "active", consumer.Status)

		active := store.ActiveConsumersForOrigin("orders")
		assert.Len(t, active, 1)
		assert.Equal(t, consumer.ID, active[0].ID)
	})

	t.Run("should return empty slice for origin with no consumers", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		// Act
		active := store.ActiveConsumersForOrigin("unknown-origin")

		// Assert
		assert.Empty(t, active)
	})
}
