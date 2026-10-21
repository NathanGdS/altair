package web

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nathangds/altair/shared"
)

// page is the template data: a Snapshot plus what the templates derive from it.
type page struct {
	Snapshot

	// Phase keeps CSS animations continuous across the once-per-second HTMX swap: every
	// animation starts at a negative delay of Phase seconds, so a freshly swapped element
	// resumes where the old one was instead of restarting.
	Phase float64

	Stages  []stage
	Links   []link
	Workers []workerInfo

	PurgeMinutes int
	HeartbeatTTL time.Duration
	RateWindow   int
}

// stage is one star in the pipeline constellation.
type stage struct {
	Label   string
	Value   string
	Note    string
	X, Y, R float64
	LabelY  float64
	Backlog bool
	Failed  bool
}

type link struct {
	X1, Y1, X2, Y2 float64
	Active         bool
}

type workerInfo struct {
	Name  string
	Every time.Duration
	Job   string
}

// stagePositions place the six stages in a 1000×260 viewBox, zig-zagging like a constellation.
var stagePositions = [][2]float64{{80, 140}, {248, 72}, {416, 150}, {584, 64}, {752, 140}, {920, 80}}

// backlogFloor is the smallest queue worth flagging as a backlog.
const backlogFloor = 100

func newPage(s Snapshot, now time.Time) page {
	p := page{
		Snapshot:     s,
		Phase:        float64(now.UnixMilli()%3_600_000) / 1000,
		PurgeMinutes: int(shared.PurgeInterval / time.Minute),
		HeartbeatTTL: shared.ConsumerHeartbeatTTL,
		RateWindow:   rateWindow,
		Workers: []workerInfo{
			{"Consumer", shared.ConsumerRunningInterval, "Moves waiting messages to processed and queues webhook calls"},
			{"Delivery", shared.DeliveryRunningInterval, fmt.Sprintf("Calls consumer webhooks, %d tries per message", shared.DeliveryMaxRetries)},
			{"Heartbeat sweeper", shared.ConsumerTTLSweepInterval, "Drops consumers silent for " + formatInterval(shared.ConsumerHeartbeatTTL)},
			{"Purge", shared.PurgeInterval, "Deletes processed messages older than " + formatInterval(shared.PurgeInterval)},
			{"Empty file cleanup", shared.RemoveEmptyFilesInterval, "Moves empty message files to trash"},
			{"Trash cleanup", shared.RemoveMakedFilesInterval, "Deletes files in trash"},
		},
	}

	deliveredNote := "since start"
	if s.FailedLines > 0 {
		deliveredNote = formatCount(s.FailedLines) + " failed"
	}

	p.Stages = []stage{
		{Label: "Published", Value: formatCount(s.Published), Note: "since start"},
		{Label: "Write buffer", Value: formatCount(s.QueueLen), Note: "of " + formatCount(s.QueueCap),
			Backlog: isBacklog(s.QueueLen, s.PublishRate)},
		{Label: "Waiting", Value: formatCount(s.Ready), Note: "in ready files",
			Backlog: isBacklog(s.Ready, s.ProcessRate)},
		{Label: "Processed", Value: formatCount(s.Processed), Note: fmt.Sprintf("kept %d min", p.PurgeMinutes)},
		{Label: "To deliver", Value: formatCount(s.DeliveryPending), Note: "webhook calls",
			Backlog: isBacklog(s.DeliveryPending, s.DeliverRate)},
		{Label: "Delivered", Value: formatCount(s.Delivered), Note: deliveredNote, Failed: s.FailedLines > 0},
	}
	values := []int{int(s.Published), s.QueueLen, s.Ready, s.Processed, s.DeliveryPending, int(s.Delivered)}
	for i := range p.Stages {
		p.Stages[i].X, p.Stages[i].Y = stagePositions[i][0], stagePositions[i][1]
		p.Stages[i].R = starRadius(values[i])
		p.Stages[i].LabelY = p.Stages[i].Y + p.Stages[i].R + 30
	}

	active := []bool{
		s.PublishRate > 0,
		s.PublishRate > 0 || s.QueueLen > 0,
		s.ProcessRate > 0,
		s.DeliveryPending > 0 || s.DeliverRate > 0,
		s.DeliverRate > 0,
	}
	for i, on := range active {
		from, to := p.Stages[i], p.Stages[i+1]
		p.Links = append(p.Links, link{X1: from.X, Y1: from.Y, X2: to.X, Y2: to.Y, Active: on})
	}

	return p
}

// isBacklog flags a queue holding more than ~10 seconds of what drains it.
func isBacklog(queued int, drainPerSecond float64) bool {
	return queued >= backlogFloor && float64(queued) > drainPerSecond*10
}

// starRadius grows logarithmically, so a million messages is a big star, not a flood.
func starRadius(v int) float64 {
	return 5 + 3*math.Log10(float64(max(v, 0))+1)
}

var templateFuncs = template.FuncMap{
	"count":     func(v any) string { return formatCount(v) },
	"rate":      formatRate,
	"bytes":     formatBytes,
	"duration":  formatDuration,
	"interval":  formatInterval,
	"percent":   func(f float64) string { return strconv.FormatFloat(f*100, 'f', 1, 64) },
	"shortID":   shortID,
	"sparkline": sparkline,
	"sparkarea": sparkarea,
	"peak":      peak,
	"fill":      func(n, total int) float64 { return fraction(n, total) * 100 },
}

func formatCount(v any) string {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case uint64:
		n = int64(x)
	case int64:
		n = x
	default:
		return fmt.Sprint(v)
	}

	digits := strconv.FormatInt(n, 10)
	negative := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")

	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

func formatRate(perSecond float64) string {
	switch {
	case perSecond == 0:
		return "0"
	case perSecond < 10:
		return strconv.FormatFloat(perSecond, 'f', 1, 64)
	default:
		return formatCount(int(math.Round(perSecond)))
	}
}

func formatBytes(b uint64) string {
	const mb = 1024 * 1024
	if b < mb {
		return strconv.FormatUint(b/1024, 10) + " KB"
	}
	return strconv.FormatFloat(float64(b)/mb, 'f', 1, 64) + " MB"
}

// formatDuration renders the two most significant units: "45s", "3m 12s", "2h 05m", "3d 4h".
func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	case s < 86400:
		return fmt.Sprintf("%dh %02dm", s/3600, s%3600/60)
	default:
		return fmt.Sprintf("%dd %dh", s/86400, s%86400/3600)
	}
}

// formatInterval renders a configured interval: "5 s", "15 min".
func formatInterval(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d min", int(d/time.Minute))
	}
	return fmt.Sprintf("%g s", d.Seconds())
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func fraction(n, total int) float64 {
	if total <= 0 {
		return 0
	}
	return min(float64(n)/float64(total), 1)
}

func peak(values []float64) float64 {
	top := 0.0
	for _, v := range values {
		top = max(top, v)
	}
	return top
}

// sparkline returns SVG polyline points for values scaled into a width×height box.
func sparkline(values []float64, width, height float64) string {
	if len(values) < 2 {
		return ""
	}
	top := max(peak(values), 1)
	step := width / float64(len(values)-1)

	var b strings.Builder
	for i, v := range values {
		if i > 0 {
			b.WriteByte(' ')
		}
		x := float64(i) * step
		y := height - (v/top)*(height-2) - 1
		b.WriteString(strconv.FormatFloat(x, 'f', 1, 64))
		b.WriteByte(',')
		b.WriteString(strconv.FormatFloat(y, 'f', 1, 64))
	}
	return b.String()
}

// sparkarea closes the sparkline into a polygon down to the baseline, for the fill under it.
func sparkarea(values []float64, width, height float64) string {
	line := sparkline(values, width, height)
	if line == "" {
		return ""
	}
	h := strconv.FormatFloat(height, 'f', 1, 64)
	w := strconv.FormatFloat(width, 'f', 1, 64)
	return "0," + h + " " + line + " " + w + "," + h
}
