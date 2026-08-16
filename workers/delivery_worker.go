package workers

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

var deliveryHTTPClient = &http.Client{
	Timeout: shared.DeliveryHTTPTimeout,
}

var deliveryBackoffSteps = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

func DeliveryWorker() {
	initDeliveryDirectories()
	shared.Log.Info("[delivery] initialized, running every " + shared.DeliveryRunningInterval.String())

	for {
		shared.Log.Info("[delivery] delivering pending messages")
		deliverPending()
		time.Sleep(shared.DeliveryRunningInterval)
	}
}

func deliverPending() {
	files, err := os.ReadDir(DeliveryPendingDir)
	if err != nil {
		shared.Log.Error("failed to read delivery pending dir", zap.Error(err))
		return
	}

	lineChan := make(chan deliveryLine, shared.DeliveryQueueCapacity)
	wg := sync.WaitGroup{}

	for range shared.ConsumerWorkingPool {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for line := range lineChan {
				deliverOne(line)
			}
		}()
	}

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		path := filepath.Join(DeliveryPendingDir, f.Name())
		lines, err := readAndTruncatePendingLines(path)
		if err != nil {
			shared.Log.Error("failed to read pending delivery file", zap.String("file", path), zap.Error(err))
			continue
		}
		for _, line := range lines {
			lineChan <- line
		}
	}

	close(lineChan)
	wg.Wait()
}

func deliverOne(line deliveryLine) {
	for attempt := 0; attempt < shared.DeliveryMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(deliveryBackoffSteps[attempt-1])
		}

		req, err := http.NewRequest(http.MethodPost, line.WebhookURL, bytes.NewBufferString(line.Payload))
		if err != nil {
			shared.Log.Error("failed to build delivery request", zap.String("consumer_id", line.ConsumerID), zap.Error(err))
			line.Attempts = attempt + 1
			line.LastError = err.Error()
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := deliveryHTTPClient.Do(req)
		line.Attempts = attempt + 1
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return
			}
			line.LastError = "unexpected status code: " + resp.Status
		} else {
			line.LastError = err.Error()
		}
	}

	shared.Log.Error("delivery failed after max retries",
		zap.String("consumer_id", line.ConsumerID),
		zap.String("message_id", line.MessageID),
		zap.String("last_error", line.LastError),
	)

	line.FailedAt = time.Now().UTC().Format(time.RFC3339)
	if err := appendDeliveryLine(failedFilePath(line.Origin), line); err != nil {
		shared.Log.Error("failed to write failed delivery record", zap.Error(err))
	}
}
