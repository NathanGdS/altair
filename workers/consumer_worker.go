package workers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/nathangds/altair/handlers"
)

var (
	processingFiles = sync.Map{}
	fileLocks       sync.Map
	initialized     bool
	initMutex       sync.Mutex
)

func ConsumerWorker() {
	log.Println("Starting consumer worker")

	// Inicializa os diretórios uma única vez
	initDirectories()

	for {
		time.Sleep(1 * time.Second)

		files, err := os.ReadDir("messages/ready")
		if err != nil {
			log.Println("Error reading directory:", err)
			continue
		}

		for _, file := range files {
			fileName := file.Name()

			// Verifica se o arquivo já está sendo processado
			if _, loaded := processingFiles.LoadOrStore(fileName, true); loaded {
				continue
			}

			processingFiles.Delete(fileName)

			log.Println("Processing file:", fileName)
			go processFile(fileName)
		}
	}
}

func initDirectories() {
	initMutex.Lock()
	defer initMutex.Unlock()

	if initialized {
		return
	}

	err := os.MkdirAll("messages/ready", os.ModePerm)
	if err != nil {
		log.Println("Error creating ready directory:", err)
		return
	}

	err = os.MkdirAll("messages/processed", os.ModePerm)
	if err != nil {
		log.Println("Error creating processed directory:", err)
		return
	}

	initialized = true
	log.Println("Directories initialized successfully")
}

func processFile(fileName string) {
	defer processingFiles.Delete(fileName)

	log.Println("Processing file:", fileName)

	filePath := fmt.Sprintf("messages/ready/%s", fileName)

	file, err := os.Open(filePath)
	if err != nil {
		log.Println("Error opening file:", err)
		return
	}

	scanner := bufio.NewScanner(file)

	wg := sync.WaitGroup{}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		wg.Add(1)

		go func(messageLine string) {
			defer wg.Done()

			var message handlers.Message
			err := json.Unmarshal([]byte(messageLine), &message)
			if err != nil {
				log.Println("Error unmarshalling line:", err)
				return
			}

			log.Println("Message Consumed:", message)

		}(line)
	}

	wg.Wait()

	// Fecha o arquivo antes de tentar movê-lo
	file.Close()

	timeNow := time.Now().Format("2006-01-02-15")
	processedFilePath := fmt.Sprintf("messages/processed/%s.json", timeNow)

	l, _ := fileLocks.LoadOrStore(processedFilePath, &sync.Mutex{})
	lock := l.(*sync.Mutex)

	lock.Lock()
	defer lock.Unlock()

	// Verifica se o arquivo já existe em processed
	processedFile, err := os.Open(processedFilePath)

	if err != nil {
		// Arquivo não existe em processed, move o arquivo inteiro
		log.Println("File not found in processed. Moving entire file.")
		err = os.Rename(filePath, processedFilePath)
		if err != nil {
			log.Println("Error moving file to processed directory:", err)
			return
		}
		log.Println("File moved to processed:", fileName)
		return
	}
	// Arquivo já existe, faz append das novas linhas
	processedFile.Close()

	// Lê o conteúdo do arquivo original
	originalContent, err := os.ReadFile(filePath)
	if err != nil {
		log.Println("Error reading original file:", err)
		return
	}

	// Abre o arquivo processed para append
	fileToAppend, err := os.OpenFile(processedFilePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("Error opening processed file for append:", err)
		return
	}
	defer fileToAppend.Close()

	// Adiciona quebra de linha se o arquivo não estiver vazio
	stat, err := fileToAppend.Stat()
	if err == nil && stat.Size() > 0 {
		_, err = fileToAppend.WriteString("\n")
		if err != nil {
			log.Println("Error writing newline:", err)
			return
		}
	}

	// Faz append do conteúdo
	_, err = fileToAppend.Write(originalContent)
	if err != nil {
		log.Println("Error appending to processed file:", err)
		return
	}

	// Remove o arquivo original
	err = os.Remove(filePath)
	if err != nil {
		log.Println("Error removing original file:", err)
		return
	}

	log.Println("File content appended to processed:", fileName)

}
