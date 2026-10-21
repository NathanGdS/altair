package shared

import (
	"sync/atomic"
	"time"
)

// StartedAt is the process start time, used by the dashboard to report uptime.
var StartedAt = time.Now()

// Process-wide monotonic counters, read by the dashboard. They only ever grow, so rates
// can be derived from deltas without being affected by purges or file truncation.
var (
	PublishedTotal      atomic.Uint64
	DeliveredTotal      atomic.Uint64
	DeliveryFailedTotal atomic.Uint64
)
