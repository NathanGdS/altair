package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

var tmpl *template.Template

func init() {
	tmpl, _ = template.ParseGlob("web/templates/*.html")
}

type Report struct {
	PurgingInterval   int
	PendingMessages   int
	ProcessedMessages int
	DeliveryProgress  float64
}

func RegisterWebHandlers() {

	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.ExecuteTemplate(w, "home.html", nil)

		if err != nil {
			http.Error(w, "Erro ao renderizar o template: "+err.Error(), http.StatusInternalServerError)
			return
		}
	})

	http.HandleFunc("GET /status-report", func(w http.ResponseWriter, r *http.Request) {
		pedingMessages, pmErr := scanAndSumLines("messages/ready")

		if pmErr != nil {
			shared.Log.Error("Error on fetching pending messages from directory", zap.Error(pmErr))
		}

		totalProcessedMessages, tpmErr := scanAndSumLines("messages/processed")

		if tpmErr != nil {
			shared.Log.Error("Error on fetching total processed messages", zap.Error(tpmErr))
		}

		totalMessages := pedingMessages + totalProcessedMessages

		percentage := (float64(totalProcessedMessages) / float64(totalMessages)) * 100

		report := Report{
			PurgingInterval:   int(shared.PurgeInterval / time.Minute),
			PendingMessages:   pedingMessages,
			ProcessedMessages: totalProcessedMessages,
			DeliveryProgress:  roundToTwoDecimalPlaces(percentage),
		}

		err := tmpl.ExecuteTemplate(w, "StatusReport", report)

		if err != nil {
			http.Error(w, "Error to renderize status report template! "+err.Error(), http.StatusInternalServerError)
		}

	})
}

func countLines(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	buf := make([]byte, 32*1024)
	count := 0

	for {
		n, err := file.Read(buf)

		count += bytes.Count(buf[:n], []byte{'\n'})

		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}

	return count, nil
}

func scanAndSumLines(rootDir string) (int, error) {
	var totalLines int
	var mu sync.Mutex

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			shared.Log.Error("Erro ao acessar caminho", zap.String("path", path), zap.Error(err))
			return nil
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}

		lines, err := countLines(path)
		if err != nil {
			shared.Log.Error("Erro ao contar linhas", zap.String("path", path), zap.Error(err))
			return nil
		}

		mu.Lock()
		totalLines += lines
		mu.Unlock()

		return nil
	})

	if err != nil {
		return 0, fmt.Errorf("erro durante a caminhada no diretório: %w", err)
	}

	return totalLines, nil
}

func roundToTwoDecimalPlaces(f float64) float64 {
	shifted := f * 100
	roundedShifted := math.Round(shifted)
	return roundedShifted / 100
}
