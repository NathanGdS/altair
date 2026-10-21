package shared

import (
	"runtime"
	"time"
)

const (
	PurgeInterval            = 15 * time.Minute
	RemoveEmptyFilesInterval = 5 * time.Minute
	ConsumerRunningInterval  = 5 * time.Second
	RemoveMakedFilesInterval = 15 * time.Minute
)

const (
	DeliveryRunningInterval = 5 * time.Second
	DeliveryQueueCapacity   = 5000
	DeliveryMaxRetries      = 3
	DeliveryHTTPTimeout     = 5 * time.Second
)

var ConsumerWorkingPool = getWorkingPool()

var (
	ConsumerHeartbeatTTL     = 30 * time.Second
	ConsumerTTLSweepInterval = 10 * time.Second
)

func getWorkingPool() int {
	// GOMAXPROCS (not NumCPU) so the pool honors container CPU limits: since Go 1.25 it is
	// derived from the cgroup CPU quota, while NumCPU always reports every host core.
	return runtime.GOMAXPROCS(0) * 2
}
