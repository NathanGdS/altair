package web

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

const (
	readyDir     = "messages/ready"
	processedDir = "messages/processed"
)

// readyFileName matches messages/ready files: "{origin}-{YYYY-MM-DD}.json".
var readyFileName = regexp.MustCompile(`^(.+)-\d{4}-\d{2}-\d{2}\.json$`)

// originFromReadyFile extracts the origin from a ready file name.
func originFromReadyFile(name string) (string, bool) {
	match := readyFileName.FindStringSubmatch(name)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// linesByOrigin counts the lines of every ready file, grouped by origin.
func linesByOrigin(dir string) map[string]int {
	out := map[string]int{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			shared.Log.Error("failed to read directory", zap.String("dir", dir), zap.Error(err))
		}
		return out
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		origin, ok := originFromReadyFile(entry.Name())
		if !ok {
			continue
		}
		lines, err := countLines(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		out[origin] += lines
	}

	return out
}

// sumLines counts the lines of every file in dir whose name contains ".json". That includes
// the delivery worker's in-flight "<file>.json.reading.<id>" files, which still hold
// undelivered lines.
func sumLines(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			shared.Log.Error("failed to read directory", zap.String("dir", dir), zap.Error(err))
		}
		return 0
	}

	total := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.Contains(entry.Name(), ".json") {
			continue
		}
		// Files can vanish between ReadDir and Open (workers rename/remove them); skip those.
		lines, err := countLines(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		total += lines
	}

	return total
}

// countLines counts the lines in a file, including a last line without a trailing newline.
func countLines(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	buf := make([]byte, 32*1024)
	count := 0
	var last byte

	for {
		n, err := file.Read(buf)
		if n > 0 {
			count += bytes.Count(buf[:n], []byte{'\n'})
			last = buf[n-1]
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}

	if last != 0 && last != '\n' {
		count++
	}

	return count, nil
}
