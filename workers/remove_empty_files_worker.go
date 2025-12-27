package workers

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/nathangds/altair/shared"
)

func RemoveEmptyFilesWorker(folderPath string) {
	log.Printf("[Remove-Empty-Files] woker innitiated! (%s)", folderPath)

	for {
		log.Printf("[Remove-Empty-Files] Removing empty files on folder (%s)", folderPath)
		markToDelete(folderPath)
		log.Println("[Remove-Empty-Files] Woker finished execution")
		time.Sleep(shared.RemoveEmptyFilesInterval)
	}
}

func DeleteMakedFiles() {
	log.Println("[Delete-Marked-Files] woker innitiated!")

	for {
		const dir = "messages/trash"
		files, err := os.ReadDir(dir)

		if err != nil {
			log.Fatal(err)
		}

		for _, f := range files {
			err := os.RemoveAll(filepath.Join(dir, f.Name()))
			if err != nil {
				log.Printf("failed to remove %s: %v", f.Name(), err)
			}
		}

		log.Println("Cleaned trash files")
		time.Sleep(10 * time.Minute)
	}
}

func markToDelete(folderPath string) {
	files, err := os.ReadDir(folderPath)
	if err != nil {
		log.Println("Error reading directory:", err)
		return
	}

	for _, file := range files {
		fileName := file.Name()
		if fileName == "" {
			continue
		}
		fullPath := filepath.Join(folderPath, file.Name())

		linesSize, err := countLines(fullPath)
		if err != nil {
			log.Fatal(err)
		}

		if linesSize <= 0 {
			err := os.Rename(fullPath, "messages/trash/"+fileName)

			if err != nil {
				log.Printf("Failed to remove file %s: %v", fullPath, err)
			}

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
