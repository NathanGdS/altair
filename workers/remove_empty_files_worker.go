package workers

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

func RemoveEmptyFilesWorker(folderPath string) {
	shared.Log.Info("[Remove-Empty-Files] worker initiated", zap.String("folder", folderPath))

	for {
		shared.Log.Info("[Remove-Empty-Files] Removing empty files", zap.String("folder", folderPath))
		markToDelete(folderPath)
		shared.Log.Info("[Remove-Empty-Files] Worker finished execution")
		time.Sleep(shared.RemoveEmptyFilesInterval)
	}
}

func DeleteMakedFiles() {
	shared.Log.Info("[Delete-Marked-Files] worker initiated")

	for {
		const dir = "messages/trash"
		files, err := os.ReadDir(dir)

		if err != nil {
			shared.Log.Fatal("error reading trash dir", zap.Error(err))
		}

		for _, f := range files {
			err := os.RemoveAll(filepath.Join(dir, f.Name()))
			if err != nil {
				shared.Log.Error("failed to remove file", zap.String("file", f.Name()), zap.Error(err))
			}
		}

		shared.Log.Info("Cleaned trash files")
		time.Sleep(shared.RemoveMakedFilesInterval)
	}
}

func markToDelete(folderPath string) {
	files, err := os.ReadDir(folderPath)
	if err != nil {
		shared.Log.Error("Error reading directory", zap.Error(err))
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
			// A read/count error here (e.g. a TOCTOU where the file vanished between ReadDir
			// and os.Open) should degrade gracefully, not take down the whole process — skip
			// this file and keep checking the rest.
			shared.Log.Error("error counting lines", zap.String("file", fullPath), zap.Error(err))
			continue
		}

		if linesSize <= 0 {
			err := os.Rename(fullPath, "messages/trash/"+fileName)

			if err != nil {
				shared.Log.Error("Failed to remove file", zap.String("path", fullPath), zap.Error(err))
			}

			shared.Log.Info("File removed for being empty", zap.String("file", "messages/processed/"+fileName))
		}
	}
}

func countLines(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

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
