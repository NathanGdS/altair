package workers

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

const (
	DeliveryPendingDir = "./deliveries/pending"
	DeliveryFailedDir  = "./deliveries/failed"
)

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

// renameWithRetry retries os.Rename a few times with a short backoff before giving up. On
// Windows, os.OpenFile's default share mode (FILE_SHARE_READ|FILE_SHARE_WRITE) does not
// include FILE_SHARE_DELETE, so a rename can transiently fail with a sharing violation if it
// races the brief window a concurrent appendDeliveryLine call has the file open. That window is
// a single OS-level write+close and normally clears in well under a millisecond, so a handful
// of short retries rides it out without deferring the whole file to the next DeliveryWorker
// tick. If every attempt fails, the file is left untouched (rename is atomic — it either fully
// renames or is a no-op), so the caller safely retries the whole file on the next tick with no
// data loss, just added latency.
func renameWithRetry(oldPath, newPath string) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Millisecond)
		}
		err = os.Rename(oldPath, newPath)
		if err == nil || os.IsNotExist(err) {
			return err
		}
	}
	return err
}

func readAndTruncatePendingLines(path string) ([]deliveryLine, error) {
	readingPath := path + ".reading"
	if err := renameWithRetry(path, readingPath); err != nil {
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
