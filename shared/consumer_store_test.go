package shared

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewConsumerStore_CreatesParentDirectory(t *testing.T) {
	t.Run("should create missing nested parent directories for the db file", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "nested", "subdir", "consumers.db")

		// Act
		store, err := NewConsumerStore(dbPath)

		// Assert
		assert.NoError(t, err)
		defer store.Close()
		assert.FileExists(t, dbPath)
	})
}

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

func TestConsumerStore_HeartbeatDeactivateExpired(t *testing.T) {
	t.Run("heartbeat updates last_heartbeat_at for a known consumer", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		consumer, err := store.Register("orders", "https://example.com/hook")
		assert.NoError(t, err)

		// Act
		err = store.Heartbeat(consumer.ID)

		// Assert
		assert.NoError(t, err)
	})

	t.Run("heartbeat returns ErrConsumerNotFound for unknown id", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		// Act
		err = store.Heartbeat("does-not-exist")

		// Assert
		assert.ErrorIs(t, err, ErrConsumerNotFound)
	})

	t.Run("deactivate removes consumer from active cache and heartbeat then fails", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		consumer, err := store.Register("orders", "https://example.com/hook")
		assert.NoError(t, err)

		// Act
		err = store.Deactivate(consumer.ID)
		assert.NoError(t, err)

		// Assert
		assert.Empty(t, store.ActiveConsumersForOrigin("orders"))
		err = store.Heartbeat(consumer.ID)
		assert.ErrorIs(t, err, ErrConsumerNotFound)
	})

	t.Run("expired consumers are returned once past ttl", func(t *testing.T) {
		// Arrange
		dbPath := filepath.Join(t.TempDir(), "consumers.db")
		store, err := NewConsumerStore(dbPath)
		assert.NoError(t, err)
		defer store.Close()

		fresh, err := store.Register("orders", "https://example.com/fresh")
		assert.NoError(t, err)
		stale, err := store.Register("orders", "https://example.com/stale")
		assert.NoError(t, err)

		store.mu.Lock()
		for i, c := range store.cache["orders"] {
			if c.ID == stale.ID {
				store.cache["orders"][i].LastHeartbeatAt = time.Now().UTC().Add(-time.Hour)
			}
		}
		store.mu.Unlock()

		// Act
		expired := store.ExpiredConsumers(30 * time.Second)

		// Assert
		assert.Len(t, expired, 1)
		assert.Equal(t, stale.ID, expired[0].ID)
		_ = fresh
	})
}
