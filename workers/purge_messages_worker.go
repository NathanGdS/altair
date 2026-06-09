package workers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/handlers"
	"github.com/nathangds/altair/shared"
)

func PurgeMessagesWorker() {
	shared.Log.Info("Starting purge messages worker")

	for {
		shared.Log.Info("Purging messages")
		purgeMessages()
		shared.Log.Info("Messages purged")
		time.Sleep(shared.PurgeInterval)
	}
}

func purgeMessages() {
	files, err := os.ReadDir("messages/processed")
	if err != nil {
		shared.Log.Error("Error reading directory", zap.Error(err))
		return
	}

	for _, file := range files {
		fileName := file.Name()

		if strings.HasSuffix(fileName, ".tmp") {
			continue
		}

		if !strings.HasSuffix(fileName, ".json") {
			continue
		}

		filePath := fmt.Sprintf("messages/processed/%s", fileName)
		file, err := os.Open(filePath)
		if err != nil {
			shared.Log.Error("Error opening file", zap.Error(err))
			continue
		}
		defer file.Close()

		removeLineFromFile(file)
	}
}

func removeLineFromFile(file *os.File) {
	originalFileName := file.Name()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var message handlers.Message
		err := json.Unmarshal([]byte(line), &message)
		if err != nil {
			shared.Log.Error("Error unmarshalling line", zap.Error(err))
			continue
		}

		if !message.ReceivedAt.Before(time.Now().Add(-shared.PurgeInterval)) {
			lines = append(lines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		shared.Log.Error("Error scanning file", zap.Error(err))
		return
	}

	file.Close()

	tempFileName := originalFileName + ".tmp"
	tempFile, err := os.Create(tempFileName)
	if err != nil {
		shared.Log.Error("Error creating temp file", zap.Error(err))
		return
	}
	defer tempFile.Close()

	for _, line := range lines {
		_, err := tempFile.WriteString(line + "\n")
		if err != nil {
			shared.Log.Error("Error writing line", zap.Error(err))
			return
		}
	}

	tempFile.Close()

	err = os.Remove(originalFileName)
	if err != nil {
		shared.Log.Error("Error removing original file", zap.Error(err))
		return
	}

	err = os.Rename(tempFileName, originalFileName)
	if err != nil {
		shared.Log.Error("Error renaming temp file", zap.Error(err))
		return
	}

	shared.Log.Info("File purged", zap.String("file", originalFileName))
}
