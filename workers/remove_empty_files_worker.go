package workers

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"time"
)

func RemoveEmptyFilesWorker() {
	log.Println("[Remove-Empty-Files] woker innitiated!")

	for {
		log.Println("[Remove-Empty-Files] Removing empty files")
		removeEmptyFiles()
		log.Println("[Remove-Empty-Files] Empty files removed")
		time.Sleep(10 * time.Second)
	}
}

func removeEmptyFiles() {
	files, err := os.ReadDir("messages/processed")
	if err != nil {
		log.Println("Error reading directory:", err)
		return
	}

	for _, file := range files {
		fileName := file.Name()
		if fileName == "" {
			continue
		}

		linesSize, err := countLines("messages/processed/" + fileName)
		if err != nil {
			log.Fatal(err)
		}

		if linesSize <= 0 {
			os.Remove("messages/processed/" + fileName)
			log.Printf("File %s removed for being empty", "messages/processed/"+fileName)
		}
	}
}

func countLines(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close() // Ensure the file is closed when the function exits

	scanner := bufio.NewScanner(file)
	lineCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		if line != "\n" && line != "" {
			lineCount++
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error during scanning: %w", err)
	}

	return lineCount, nil
}
