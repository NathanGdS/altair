package workers

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

const (
	DeliveryPendingDir = "./deliveries/pending"
	DeliveryFailedDir  = "./deliveries/failed"
)

// deliveryFileMu serializes every operation that touches a pending delivery file's identity:
// appendDeliveryLine's open-append-close, and readAndTruncatePendingLines' rename. This closes
// both the writer-vs-writer race (concurrent appendDeliveryLine calls to the same path can hit
// a Windows sharing violation against each other) and the writer-vs-renamer race (a writer's
// OpenFile racing the reader's rename) with a single simple primitive. One global mutex, not
// per-path: this broker is a local single-process system, not high-concurrency-across-many-
// origins, so the extra complexity of a per-path lock map isn't warranted. It only serializes
// small local file appends/renames — the DeliveryWorker's HTTP POST worker pool (the actual
// network calls, with up to 5s+ retries) is untouched and stays fully concurrent.
var deliveryFileMu sync.Mutex

type deliveryLine struct {
	MessageID  string `json:"message_id"`
	Origin     string `json:"origin"`
	ConsumerID string `json:"consumer_id"`
	WebhookURL string `json:"webhook_url"`
	Payload    string `json:"payload"`
	Attempts   int    `json:"attempts"`
	LastError  string `json:"last_error,omitempty"`
	FailedAt   string `json:"failed_at,omitempty"`
}

type messageEnvelope struct {
	Origin string `json:"origin"`
	Id     string `json:"id"`
}

func initDeliveryDirectories() {
	dirs := []string{DeliveryPendingDir, DeliveryFailedDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			shared.Log.Error("Error creating directory", zap.String("dir", dir), zap.Error(err))
		}
	}
}

func pendingFilePath(origin string) string {
	fileName := time.Now().Format("2006-01-02")
	return filepath.Join(DeliveryPendingDir, origin+"-"+fileName+".json")
}

func failedFilePath(origin string) string {
	fileName := time.Now().Format("2006-01-02")
	return filepath.Join(DeliveryFailedDir, origin+"-"+fileName+".json")
}

func appendDeliveryLine(path string, line deliveryLine) error {
	jsonBytes, err := json.Marshal(line)
	if err != nil {
		return err
	}

	deliveryFileMu.Lock()
	defer deliveryFileMu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(jsonBytes, '\n'))
	return err
}

func enqueueDeliveries(messageID, origin, payload string, consumers []shared.Consumer) {
	if len(consumers) == 0 {
		return
	}

	path := pendingFilePath(origin)
	for _, c := range consumers {
		line := deliveryLine{
			MessageID:  messageID,
			Origin:     origin,
			ConsumerID: c.ID,
			WebhookURL: c.WebhookURL,
			Payload:    payload,
		}
		if err := appendDeliveryLine(path, line); err != nil {
			shared.Log.Error("failed to enqueue delivery", zap.String("consumer_id", c.ID), zap.Error(err))
		}
	}
}

func readAndTruncatePendingLines(path string) ([]deliveryLine, error) {
	readingPath := path + ".reading"

	// Hold deliveryFileMu around just the rename: appendDeliveryLine holds the same mutex for
	// its entire OpenFile-Write-Close sequence (and closes the file before releasing the
	// mutex), so by the time this goroutine acquires the lock, no writer can have the file
	// open. That's what makes the rename provably immune to the Windows sharing-violation this
	// used to hit — os.OpenFile's default share mode (FILE_SHARE_READ|FILE_SHARE_WRITE) omits
	// FILE_SHARE_DELETE, so a rename against a concurrently-open handle used to fail
	// transiently; with the mutex, that handle can no longer exist at rename time, so the
	// earlier retry-around-the-rename workaround is no longer needed. Release the lock before
	// opening/scanning/removing so it's held as briefly as possible.
	deliveryFileMu.Lock()
	err := os.Rename(path, readingPath)
	deliveryFileMu.Unlock()

	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	file, err := os.Open(readingPath)
	if err != nil {
		return nil, err
	}

	var lines []deliveryLine
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var line deliveryLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			shared.Log.Error("failed to parse delivery line", zap.Error(err))
			continue
		}
		lines = append(lines, line)
	}
	scanErr := scanner.Err()
	file.Close()

	if err := os.Remove(readingPath); err != nil {
		shared.Log.Error("failed to remove processed delivery file", zap.String("file", readingPath), zap.Error(err))
	}

	return lines, scanErr
}

func parseOriginAndID(line string) (origin string, id string) {
	var env messageEnvelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		shared.Log.Error("failed to parse message for delivery routing", zap.Error(err))
		return "", ""
	}
	return env.Origin, env.Id
}
