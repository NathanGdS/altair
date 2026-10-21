package web

import (
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/nathangds/altair/handlers"
	"github.com/nathangds/altair/shared"
	"github.com/nathangds/altair/workers"
)

// historySize is how many one-second samples the rate sparklines keep.
const historySize = 60

// rateWindow is how many recent samples the displayed rates average over. The consumer and
// delivery workers emit their output in bursts every few seconds (longer while webhook retries
// back off), so a short window would mostly read zero between bursts.
const rateWindow = 30

// series is a fixed-size ring of samples, oldest first.
type series struct {
	values []float64
}

func newSeries(size int) *series {
	return &series{values: make([]float64, size)}
}

func (s *series) push(v float64) {
	copy(s.values, s.values[1:])
	s.values[len(s.values)-1] = v
}

func (s *series) snapshot() []float64 {
	out := make([]float64, len(s.values))
	copy(out, s.values)
	return out
}

// recentMean averages the last n samples.
func (s *series) recentMean(n int) float64 {
	if n > len(s.values) {
		n = len(s.values)
	}
	sum := 0.0
	for _, v := range s.values[len(s.values)-n:] {
		sum += v
	}
	return sum / float64(n)
}

// counters is the raw state read from disk, the consumer store and process counters.
type counters struct {
	Published      uint64
	Delivered      uint64
	DeliveryFailed uint64
	Processed      int
}

// Snapshot is everything the dashboard shows, computed once per tick and shared by every
// open dashboard, so the number of viewers never multiplies the disk scans.
type Snapshot struct {
	At     time.Time
	Uptime time.Duration

	Goroutines int
	HeapBytes  uint64
	MaxProcs   int
	PoolSize   int

	Published      uint64
	Delivered      uint64
	DeliveryFailed uint64

	QueueLen int
	QueueCap int

	Ready           int
	Processed       int
	DeliveryPending int
	FailedLines     int

	PublishRate float64
	ProcessRate float64
	DeliverRate float64

	PublishHistory []float64
	DeliverHistory []float64

	Origins   []OriginStat
	Consumers []ConsumerStat
}

type OriginStat struct {
	Name      string
	Ready     int
	Consumers int
}

type ConsumerStat struct {
	ID            string
	Origin        string
	WebhookURL    string
	HeartbeatAge  time.Duration
	HeartbeatUsed float64 // fraction of ConsumerHeartbeatTTL already elapsed, 0..1
}

// Monitor samples the broker once per tick and keeps the latest Snapshot.
type Monitor struct {
	mu     sync.RWMutex
	latest Snapshot

	hasPrev bool
	prev    counters
	prevAt  time.Time

	publishRates *series
	processRates *series
	deliverRates *series
}

func NewMonitor() *Monitor {
	return &Monitor{
		publishRates: newSeries(historySize),
		processRates: newSeries(historySize),
		deliverRates: newSeries(historySize),
	}
}

// Run ticks forever at the given interval.
func (m *Monitor) Run(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for now := range ticker.C {
		m.Tick(now)
	}
}

func (m *Monitor) Latest() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latest
}

// Tick reads the current broker state and records rates since the previous tick.
func (m *Monitor) Tick(now time.Time) {
	readyByOrigin := linesByOrigin(readyDir)
	current := counters{
		Published:      shared.PublishedTotal.Load(),
		Delivered:      shared.DeliveredTotal.Load(),
		DeliveryFailed: shared.DeliveryFailedTotal.Load(),
		Processed:      sumLines(processedDir),
	}

	snap := Snapshot{
		At:              now,
		Uptime:          now.Sub(shared.StartedAt),
		Published:       current.Published,
		Delivered:       current.Delivered,
		DeliveryFailed:  current.DeliveryFailed,
		Processed:       current.Processed,
		DeliveryPending: sumLines(workers.DeliveryPendingDir),
		FailedLines:     sumLines(workers.DeliveryFailedDir),
		MaxProcs:        runtime.GOMAXPROCS(0),
		PoolSize:        shared.ConsumerWorkingPool,
		Goroutines:      runtime.NumGoroutine(),
	}
	for _, lines := range readyByOrigin {
		snap.Ready += lines
	}
	snap.QueueLen, snap.QueueCap = handlers.WriterQueueDepth()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	snap.HeapBytes = mem.HeapAlloc

	consumers := activeConsumers()
	snap.Consumers = consumerStats(consumers, now)
	snap.Origins = originStats(readyByOrigin, consumers)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hasPrev {
		if seconds := now.Sub(m.prevAt).Seconds(); seconds > 0 {
			m.publishRates.push(rate(float64(current.Published)-float64(m.prev.Published), seconds))
			// Processed lines drop when old messages are purged; that is not negative throughput.
			m.processRates.push(rate(float64(current.Processed-m.prev.Processed), seconds))
			m.deliverRates.push(rate(float64(current.Delivered)-float64(m.prev.Delivered), seconds))
		}
	}
	m.hasPrev = true
	m.prev = current
	m.prevAt = now

	snap.PublishRate = m.publishRates.recentMean(rateWindow)
	snap.ProcessRate = m.processRates.recentMean(rateWindow)
	snap.DeliverRate = m.deliverRates.recentMean(rateWindow)
	snap.PublishHistory = m.publishRates.snapshot()
	snap.DeliverHistory = m.deliverRates.snapshot()

	m.latest = snap
}

func rate(delta, seconds float64) float64 {
	if delta <= 0 {
		return 0
	}
	return delta / seconds
}

func activeConsumers() []shared.Consumer {
	if shared.Consumers == nil {
		return nil
	}
	return shared.Consumers.ActiveConsumers()
}

// consumerStats orders consumers by origin, then by most overdue heartbeat first.
func consumerStats(consumers []shared.Consumer, now time.Time) []ConsumerStat {
	out := make([]ConsumerStat, 0, len(consumers))
	for _, c := range consumers {
		age := max(now.Sub(c.LastHeartbeatAt), 0)
		out = append(out, ConsumerStat{
			ID:            c.ID,
			Origin:        c.Origin,
			WebhookURL:    c.WebhookURL,
			HeartbeatAge:  age,
			HeartbeatUsed: min(float64(age)/float64(shared.ConsumerHeartbeatTTL), 1),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin < out[j].Origin
		}
		return out[i].HeartbeatAge > out[j].HeartbeatAge
	})
	return out
}

// originStats merges origins with waiting messages and origins with consumers, busiest first.
func originStats(readyByOrigin map[string]int, consumers []shared.Consumer) []OriginStat {
	byName := map[string]*OriginStat{}
	get := func(name string) *OriginStat {
		if stat, ok := byName[name]; ok {
			return stat
		}
		stat := &OriginStat{Name: name}
		byName[name] = stat
		return stat
	}

	for name, lines := range readyByOrigin {
		get(name).Ready += lines
	}
	for _, c := range consumers {
		get(c.Origin).Consumers++
	}

	out := make([]OriginStat, 0, len(byName))
	for _, stat := range byName {
		out = append(out, *stat)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ready != out[j].Ready {
			return out[i].Ready > out[j].Ready
		}
		return out[i].Name < out[j].Name
	})
	return out
}
