package workers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/nathangds/altair/handlers"
)

var (
	processingFiles sync.Map // avoid double processments
	fileLocks       sync.Map // 1 lock for "processed" destiny
	onceInit        sync.Once
	messagePool     = sync.Pool{
		New: func() any { return new(handlers.Message) },
	}
)

func ConsumerWorker() {
	log.Println("Starting consumer worker")
	onceInit.Do(func() {
		initDirectories()
	})

	for {
		time.Sleep(1 * time.Second)

		files, err := os.ReadDir("messages/ready")
		if err != nil {
			log.Println("Error reading directory:", err)
			continue
		}

		for _, file := range files {
			fileName := file.Name()

			// Check if this file is already being processed
			if _, loaded := processingFiles.LoadOrStore(fileName, true); loaded {
				continue
			}

			go processFile(fileName)
		}
	}
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

func processFile(fileName string) {
	defer processingFiles.Delete(fileName)

	filePath := fmt.Sprintf("messages/ready/%s", fileName)
	log.Println("Processing file:", filePath)

	file, err := os.Open(filePath)
	if err != nil {
		log.Println("Error opening file:", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	workerLimit := make(chan struct{}, runtime.NumCPU()*4) // process 50 lines simultaneously
	var wg sync.WaitGroup

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		workerLimit <- struct{}{}
		wg.Add(1)

		go func(messageLine string) {
			defer wg.Done()
			defer func() { <-workerLimit }()

			msg := messagePool.Get().(*handlers.Message)
			defer messagePool.Put(msg)

			// reset
			*msg = handlers.Message{}

			if err := json.Unmarshal([]byte(messageLine), msg); err != nil {
				log.Println("Error unmarshalling line:", err)
				return
			}

			log.Println("Message Consumed:", *msg)
		}(line)
	}

	wg.Wait()
	file.Close()

	timeNow := time.Now().Format("2006-01-02-15")
	destFile := fmt.Sprintf("messages/processed/%s.json", timeNow)

	// Lock target file
	line, _ := fileLocks.LoadOrStore(destFile, &sync.Mutex{})
	lock := line.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	// verify if already exists
	_, err = os.Stat(destFile)
	if os.IsNotExist(err) {
		// First file -> only move it
		if err := os.Rename(filePath, destFile); err != nil {
			log.Println("Error moving file:", err)
			return
		}
		log.Println("File moved to processed:", fileName)
		return
	}

	// Append if already exists
	originalContent, err := os.ReadFile(filePath)
	if err != nil {
		log.Println("Error reading original file:", err)
		return
	}

	fileToAppend, err := os.OpenFile(destFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("Error opening processed file:", err)
		return
	}
	defer fileToAppend.Close()

	writer := bufio.NewWriter(fileToAppend)

	// add line break on the end
	stat, _ := fileToAppend.Stat()
	if stat.Size() > 0 {
		writer.WriteString("\n")
	}

	writer.Write(originalContent)
	writer.Flush()

	os.Remove(filePath)

	log.Println("File content appended:", fileName)
}
