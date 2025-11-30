package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

		totalProcessedMessages, tpmErr := scanAndSumLines("messages/processed")

		if tpmErr != nil {
			log.Println("Error on fetching total processed messages! " + tpmErr.Error())
		}

		report := Report{
			PurgingInterval:   int(shared.PurgeInterval / time.Minute),
			PendingMessages:   pedingMessages,
			ProcessedMessages: totalProcessedMessages,
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

// countLines lê o arquivo e conta o número de quebras de linha ('\n').
// Esta é uma maneira eficiente de contar linhas em Go, usando um buffer.
func countLines(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	// Define um buffer grande para leitura (32KB, por exemplo)
	buf := make([]byte, 32*1024)
	count := 0

	for {
		// Lê um bloco de bytes
		n, err := file.Read(buf)

		// Conta o número de '\n' no bloco lido
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

// scanAndSumLines percorre o diretório fornecido e retorna a soma total das linhas.
func scanAndSumLines(rootDir string) (int, error) {
	var totalLines int
	var mu sync.Mutex

	// filepath.Walk percorre recursivamente o diretório
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Printf("Erro ao acessar um caminho %q: %v\n", path, err)
			return nil
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(info.Name(), ".json") {
			return nil // Ignora arquivos que não são .json
		}

		// Ponto 1 & 2: Conta as linhas no arquivo
		lines, err := countLines(path)
		if err != nil {
			// Loga o erro, mas continua para o próximo arquivo
			fmt.Printf("Erro ao contar linhas em %q: %v\n", path, err)
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
