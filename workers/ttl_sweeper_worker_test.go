package workers

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/nathangds/altair/shared"
)

func TestSweepExpiredConsumers(t *testing.T) {
	t.Run("deactivates a consumer whose heartbeat is older than the ttl", func(t *testing.T) {
		// Arrange
		originalTTL := shared.ConsumerHeartbeatTTL
		shared.ConsumerHeartbeatTTL = 50 * time.Millisecond
		defer func() { shared.ConsumerHeartbeatTTL = originalTTL }()

		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		stale, err := shared.Consumers.Register("orders", "https://example.com/stale")
		assert.NoError(t, err)

		time.Sleep(100 * time.Millisecond)

		// Act
		sweepExpiredConsumers()

		// Assert
		assert.Empty(t, shared.Consumers.ActiveConsumersForOrigin("orders"))
		err = shared.Consumers.Heartbeat(stale.ID)
		assert.ErrorIs(t, err, shared.ErrConsumerNotFound)
	})

	t.Run("leaves a consumer with a recent heartbeat active", func(t *testing.T) {
		// Arrange
		originalTTL := shared.ConsumerHeartbeatTTL
		shared.ConsumerHeartbeatTTL = time.Minute
		defer func() { shared.ConsumerHeartbeatTTL = originalTTL }()

		err := shared.InitConsumerStore(filepath.Join(t.TempDir(), "consumers.db"))
		assert.NoError(t, err)
		defer shared.Consumers.Close()

		fresh, err := shared.Consumers.Register("orders", "https://example.com/fresh")
		assert.NoError(t, err)

		// Act
		sweepExpiredConsumers()

		// Assert
		active := shared.Consumers.ActiveConsumersForOrigin("orders")
		assert.Len(t, active, 1)
		assert.Equal(t, fresh.ID, active[0].ID)
	})
}
