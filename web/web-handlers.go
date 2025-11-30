package web

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

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
		pedingMessages, pmErr := countFilesInDirectory("messages/ready")

		if pmErr != nil {
			log.Println("Error on fetching peding messages from directory: " + pmErr.Error())
		}

		report := Report{
			PurgingInterval:   int(shared.PurgeInterval / time.Minute),
			PendingMessages:   pedingMessages,
			ProcessedMessages: 0,
		}

		err := tmpl.ExecuteTemplate(w, "StatusReport", report)

		if err != nil {
			http.Error(w, "Error to renderize status report template! "+err.Error(), http.StatusInternalServerError)
		}

	})
}

func countFilesInDirectory(dirPath string) (int, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read directory: %w", err)
	}

	fileCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			fileCount++
		}
	}
	return fileCount, nil
}
