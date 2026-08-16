package workers

import (
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

func TTLSweeperWorker() {
	shared.Log.Info("[ttl-sweeper] initialized")

	for {
		sweepExpiredConsumers()
		time.Sleep(shared.ConsumerTTLSweepInterval)
	}
}

func sweepExpiredConsumers() {
	if shared.Consumers == nil {
		return
	}

	expired := shared.Consumers.ExpiredConsumers(shared.ConsumerHeartbeatTTL)
	for _, c := range expired {
		if err := shared.Consumers.Deactivate(c.ID); err != nil {
			shared.Log.Error("failed to deactivate expired consumer", zap.String("consumer_id", c.ID), zap.Error(err))
			continue
		}
		shared.Log.Info("deactivated expired consumer", zap.String("consumer_id", c.ID), zap.String("origin", c.Origin))
	}
}
