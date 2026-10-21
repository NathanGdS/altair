package web

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/nathangds/altair/shared"
)

func TestSeries(t *testing.T) {
	t.Run("keeps the newest values in order and averages the most recent ones", func(t *testing.T) {
		// Arrange
		s := newSeries(3)

		// Act
		for _, v := range []float64{1, 2, 3, 4} {
			s.push(v)
		}

		// Assert
		assert.Equal(t, []float64{2, 3, 4}, s.snapshot())
		assert.Equal(t, 3.5, s.recentMean(2))
		assert.Equal(t, 3.0, s.recentMean(10))
	})
}

func TestMonitorTick(t *testing.T) {
	t.Run("reads pending, processed and delivery files from disk", func(t *testing.T) {
		// Arrange
		t.Chdir(t.TempDir())
		writeFile(t, "messages/ready/orders-2026-10-20.json", "1\n2")
		writeFile(t, "messages/processed/20261020_14.json", "1\n2\n3\n")
		writeFile(t, "deliveries/pending/orders-2026-10-20.json", "1\n")
		writeFile(t, "deliveries/failed/orders-2026-10-20.json", "1\n2\n")
		monitor := NewMonitor()

		// Act
		monitor.Tick(time.Now())
		snap := monitor.Latest()

		// Assert
		assert.Equal(t, 2, snap.Ready)
		assert.Equal(t, 3, snap.Processed)
		assert.Equal(t, 1, snap.DeliveryPending)
		assert.Equal(t, 2, snap.FailedLines)
		assert.Equal(t, []OriginStat{{Name: "orders", Ready: 2}}, snap.Origins)
		assert.Equal(t, 200000, snap.QueueCap)
		assert.Positive(t, snap.Goroutines)
	})

	t.Run("derives the publish rate from the counter delta between ticks", func(t *testing.T) {
		// Arrange
		t.Chdir(t.TempDir())
		monitor := NewMonitor()
		start := time.Now()
		monitor.Tick(start)

		// Act
		shared.PublishedTotal.Add(20)
		monitor.Tick(start.Add(2 * time.Second))
		snap := monitor.Latest()

		// Assert
		history := snap.PublishHistory
		assert.Len(t, history, historySize)
		assert.Equal(t, 10.0, history[len(history)-1])
		assert.InDelta(t, 10.0/rateWindow, snap.PublishRate, 0.001) // one 10/s sample averaged over the window
	})

	t.Run("never reports a negative rate when processed files are purged", func(t *testing.T) {
		// Arrange
		t.Chdir(t.TempDir())
		writeFile(t, "messages/processed/20261020_14.json", "1\n2\n3\n")
		monitor := NewMonitor()
		start := time.Now()
		monitor.Tick(start)
		writeFile(t, "messages/processed/20261020_14.json", "")

		// Act
		monitor.Tick(start.Add(time.Second))

		// Assert
		assert.Equal(t, 0.0, monitor.Latest().ProcessRate)
	})
}

func TestConsumerAndOriginStats(t *testing.T) {
	t.Run("merges ready origins with consumer origins, busiest first", func(t *testing.T) {
		// Arrange
		now := time.Now()
		consumers := []shared.Consumer{
			{ID: "c1", Origin: "payments", LastHeartbeatAt: now.Add(-3 * time.Second)},
			{ID: "c2", Origin: "orders", LastHeartbeatAt: now.Add(-1 * time.Second)},
			{ID: "c3", Origin: "orders", LastHeartbeatAt: now.Add(-24 * time.Second)},
		}

		// Act
		origins := originStats(map[string]int{"orders": 5, "audit": 9}, consumers)
		stats := consumerStats(consumers, now)

		// Assert
		assert.Equal(t, []OriginStat{
			{Name: "audit", Ready: 9},
			{Name: "orders", Ready: 5, Consumers: 2},
			{Name: "payments", Consumers: 1},
		}, origins)

		assert.Equal(t, []string{"c3", "c2", "c1"}, []string{stats[0].ID, stats[1].ID, stats[2].ID})
		assert.InDelta(t, 24.0/30.0, stats[0].HeartbeatUsed, 0.01)
	})

	t.Run("caps heartbeat usage at the TTL", func(t *testing.T) {
		// Arrange
		now := time.Now()
		consumers := []shared.Consumer{{ID: "c1", Origin: "o", LastHeartbeatAt: now.Add(-time.Hour)}}

		// Act
		stats := consumerStats(consumers, now)

		// Assert
		assert.Equal(t, 1.0, stats[0].HeartbeatUsed)
	})
}

func TestMonitorTick_ReadsConsumerStore(t *testing.T) {
	t.Run("lists consumers from the process-wide store", func(t *testing.T) {
		// Arrange
		t.Chdir(t.TempDir())
		store, err := shared.NewConsumerStore(filepath.Join(t.TempDir(), "c.db"))
		assert.NoError(t, err)
		defer store.Close()
		previous := shared.Consumers
		shared.Consumers = store
		t.Cleanup(func() { shared.Consumers = previous })
		_, err = store.Register("orders", "http://localhost:9090/hook")
		assert.NoError(t, err)
		monitor := NewMonitor()

		// Act
		monitor.Tick(time.Now())
		snap := monitor.Latest()

		// Assert
		assert.Len(t, snap.Consumers, 1)
		assert.Equal(t, "orders", snap.Consumers[0].Origin)
		assert.Equal(t, []OriginStat{{Name: "orders", Consumers: 1}}, snap.Origins)
	})
}
