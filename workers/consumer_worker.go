package workers

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nathangds/altair/shared"
)

// CONFIG
const (
	ReadDir      = "./messages/ready"
	ProcessedDir = "./messages/processed"
)

type message struct {
	FileName string
	Line     string
}

func ConsumerWorker() {
	initDirectories()
	go startConsumerLoop()
}

func startConsumerLoop() {
	fmt.Printf("[consumer] innitialized, running every %d second", shared.ConsumerRunningInterval)

	for {
		processMessages()
		time.Sleep(shared.ConsumerRunningInterval)
	}
}

func processMessages() {
	files, err := os.ReadDir(ReadDir)
	if err != nil {
		fmt.Println("erro ao ler read/:", err)
		return
	}

	msgChan := make(chan message, 5000)
	wg := sync.WaitGroup{}

	// WORKER POOL
	for range shared.ConsumerWorkingPool {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range msgChan {
				processSingleMessage(msg)
			}
		}()
	}

	// Produce messages
	for _, f := range files {
		if f.IsDir() {
			continue
		}

		fullPath := filepath.Join(ReadDir, f.Name())
		readMessagesFromFile(fullPath, msgChan)
	}

	close(msgChan)
	wg.Wait()
}

func readMessagesFromFile(path string, msgChan chan<- message) {
	file, err := os.Open(path)
	if err != nil {
		fmt.Println("[ERRO] ao abrir arquivo:", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			msgChan <- message{FileName: path, Line: line}
		}
	}

	// Zerar o arquivo após leitura
	_ = os.Truncate(path, 0)
}

func processSingleMessage(msg message) {
	// TODO: handle http delivery in future
	saveProcessed(msg.Line)
}

func saveProcessed(line string) {
	now := time.Now()
	hourFolder := now.Format("20060102_15") // exemplo: 20251207_18

	dir := filepath.Join(ProcessedDir, hourFolder)
	os.MkdirAll(dir, 0755)

	filePath := filepath.Join(dir, "processed.json")
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("[ERRO] ao salvar processed:", err)
		return
	}
	defer f.Close()

	f.WriteString(line + "\n")
}

func initDirectories() {
	dirs := []string{"messages/ready", "messages/processed"}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			log.Println("Error creating directory:", dir, err)
		}
	}
	log.Println("Directories initialized successfully")
}
