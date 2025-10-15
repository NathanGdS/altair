package workers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/nathangds/altair/handlers"
)

const purgeIntervalInMinutes = 10 * time.Minute

func PurgeMessagesWorker() {
	log.Println("Starting purge messages worker")

	for {
		log.Println("Purging messages")
		purgeMessages()
		log.Println("Messages purged")
		time.Sleep(purgeIntervalInMinutes)
	}
}

func purgeMessages() {
	files, err := os.ReadDir("messages/processed")
	if err != nil {
		log.Println("Error reading directory:", err)
		return
	}

	for _, file := range files {
		fileName := file.Name()

		// Ignora arquivos temporários (.tmp)
		if strings.HasSuffix(fileName, ".tmp") {
			continue
		}

		// Processa apenas arquivos .json
		if !strings.HasSuffix(fileName, ".json") {
			continue
		}

		filePath := fmt.Sprintf("messages/processed/%s", fileName)
		file, err := os.Open(filePath)
		if err != nil {
			log.Println("Error opening file:", err)
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
			log.Println("Error unmarshalling line:", err)
			continue
		}

		if !message.ReceivedAt.Before(time.Now().Add(-purgeIntervalInMinutes)) {
			lines = append(lines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Println("Error scanning file:", err)
		return
	}

	// Fecha o arquivo original antes de criar o temporário
	file.Close()

	if len(lines) == 0 {
		return
	}

	tempFileName := originalFileName + ".tmp"
	tempFile, err := os.Create(tempFileName)
	if err != nil {
		log.Println("Error creating temp file:", err)
		return
	}
	defer tempFile.Close()

	for _, line := range lines {
		_, err := tempFile.WriteString(line + "\n")
		if err != nil {
			log.Println("Error writing line:", err)
			return
		}
	}

	// Fecha o arquivo temporário antes de fazer as operações de rename
	tempFile.Close()

	// Remove o arquivo original
	err = os.Remove(originalFileName)
	if err != nil {
		log.Println("Error removing original file:", err)
		return
	}

	// Renomeia o arquivo temporário para o nome original
	err = os.Rename(tempFileName, originalFileName)
	if err != nil {
		log.Println("Error renaming temp file:", err)
		return
	}

	log.Println("File purged:", originalFileName)
}
