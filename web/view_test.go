package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatting(t *testing.T) {
	t.Run("formats counts with thousands separators", func(t *testing.T) {
		assert.Equal(t, "0", formatCount(0))
		assert.Equal(t, "999", formatCount(999))
		assert.Equal(t, "1,000", formatCount(1000))
		assert.Equal(t, "1,234,567", formatCount(uint64(1234567)))
		assert.Equal(t, "-12,345", formatCount(-12345))
	})

	t.Run("formats rates with one decimal only when small", func(t *testing.T) {
		assert.Equal(t, "0", formatRate(0))
		assert.Equal(t, "2.5", formatRate(2.46))
		assert.Equal(t, "1,250", formatRate(1249.6))
	})

	t.Run("formats durations with the two most significant units", func(t *testing.T) {
		assert.Equal(t, "45s", formatDuration(45*time.Second))
		assert.Equal(t, "3m 07s", formatDuration(3*time.Minute+7*time.Second))
		assert.Equal(t, "2h 05m", formatDuration(2*time.Hour+5*time.Minute))
		assert.Equal(t, "3d 4h", formatDuration(76*time.Hour))
	})

	t.Run("formats configured intervals", func(t *testing.T) {
		assert.Equal(t, "5 s", formatInterval(5*time.Second))
		assert.Equal(t, "15 min", formatInterval(15*time.Minute))
		assert.Equal(t, "90 s", formatInterval(90*time.Second))
	})

	t.Run("formats bytes", func(t *testing.T) {
		assert.Equal(t, "512 KB", formatBytes(512*1024))
		assert.Equal(t, "12.5 MB", formatBytes(12*1024*1024+512*1024))
	})
}

func TestSparkline(t *testing.T) {
	t.Run("scales values into the box, peak at the top", func(t *testing.T) {
		// Act
		points := sparkline([]float64{0, 5, 10}, 100, 22)

		// Assert
		assert.Equal(t, "0.0,21.0 50.0,11.0 100.0,1.0", points)
	})

	t.Run("keeps an all-zero series flat on the baseline", func(t *testing.T) {
		// Act
		points := sparkline([]float64{0, 0}, 10, 10)

		// Assert
		assert.Equal(t, "0.0,9.0 10.0,9.0", points)
	})

	t.Run("closes the area down to the baseline", func(t *testing.T) {
		// Act
		area := sparkarea([]float64{0, 0}, 10, 10)

		// Assert
		assert.Equal(t, "0,10.0 0.0,9.0 10.0,9.0 10.0,10.0", area)
	})
}

func TestNewPage(t *testing.T) {
	t.Run("flags a stage as backlog when it holds more than ten seconds of drain", func(t *testing.T) {
		// Arrange
		snap := Snapshot{Ready: 5000, ProcessRate: 100, DeliveryPending: 500, DeliverRate: 100}

		// Act
		p := newPage(snap, time.Now())

		// Assert
		assert.Len(t, p.Stages, 6)
		assert.True(t, p.Stages[2].Backlog, "waiting")
		assert.False(t, p.Stages[4].Backlog, "to deliver")
	})

	t.Run("does not flag a small queue even when nothing drains it", func(t *testing.T) {
		// Act
		p := newPage(Snapshot{Ready: 99}, time.Now())

		// Assert
		assert.False(t, p.Stages[2].Backlog)
	})

	t.Run("animates only the links that carry traffic", func(t *testing.T) {
		// Act
		p := newPage(Snapshot{PublishRate: 3}, time.Now())

		// Assert
		assert.Len(t, p.Links, 5)
		assert.Equal(t, []bool{true, true, false, false, false},
			[]bool{p.Links[0].Active, p.Links[1].Active, p.Links[2].Active, p.Links[3].Active, p.Links[4].Active})
	})

	t.Run("notes failed deliveries on the last stage", func(t *testing.T) {
		// Act
		p := newPage(Snapshot{FailedLines: 1200}, time.Now())

		// Assert
		assert.True(t, p.Stages[5].Failed)
		assert.Equal(t, "1,200 failed", p.Stages[5].Note)
	})
}

func TestRoutes(t *testing.T) {
	newServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		t.Chdir(t.TempDir())
		writeFile(t, "messages/ready/orders-2026-10-20.json", "1\n2")
		monitor := NewMonitor()
		monitor.Tick(time.Now())
		mux := http.NewServeMux()
		registerRoutes(mux, monitor)
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		return server
	}

	get := func(t *testing.T, url string) (int, string) {
		t.Helper()
		resp, err := http.Get(url)
		assert.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	t.Run("renders the full page with the live report inside", func(t *testing.T) {
		// Arrange
		server := newServer(t)

		// Act
		status, body := get(t, server.URL+"/")

		// Assert
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, `hx-get="/status-report"`)
		assert.Contains(t, body, "Message flow")
		assert.Contains(t, body, "orders")
	})

	t.Run("renders the report fragment without the page shell", func(t *testing.T) {
		// Arrange
		server := newServer(t)

		// Act
		status, body := get(t, server.URL+"/status-report")

		// Assert
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "Message flow")
		assert.NotContains(t, body, "<html")
		assert.NotContains(t, body, "NaN")
		assert.NotContains(t, body, "ZgotmplZ")
	})

	t.Run("serves the stylesheet", func(t *testing.T) {
		// Arrange
		server := newServer(t)

		// Act
		status, body := get(t, server.URL+"/static/altair.css")

		// Assert
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "--sky")
	})

	t.Run("returns 404 for unknown paths instead of the dashboard", func(t *testing.T) {
		// Arrange
		server := newServer(t)

		// Act
		status, _ := get(t, server.URL+"/nope")

		// Assert
		assert.Equal(t, http.StatusNotFound, status)
	})
}
