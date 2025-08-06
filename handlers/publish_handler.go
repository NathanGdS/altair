package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

type Message struct {
	Id         string         `json:"id"`
	Data       map[string]any `json:"data"`
	ReceivedAt time.Time      `json:"received_at"`
}

func (m *Message) Instantiate() {
	m.Id = uuid.New().String()
	m.ReceivedAt = time.Now().UTC()
}

func PublishHandler(w http.ResponseWriter, r *http.Request) {
	message := Message{}

	json.NewDecoder(r.Body).Decode(&message)

	message.Instantiate()

	fmt.Println(message)

	jsonBytes, err := json.Marshal(message)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	go appendOnFile(jsonBytes, 0)

	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

func appendOnFile(message []byte, retry int) {

	if retry > 3 {
		fmt.Println("Failed to append message to file after 3 retries")
		return
	}

	// create file based on the current date
	fileName := time.Now().Format("2006-01-02")
	filePath := fmt.Sprintf("messages/%s.json", fileName)

	// create the directory if it doesn't exist
	err := os.MkdirAll("messages", os.ModePerm)
	if err != nil {
		fmt.Println("Error creating directory:", err)
		appendOnFile(message, retry+1)
		return
	}

	// open file with proper flags for append
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		fmt.Println("Error opening file:", err)
		appendOnFile(message, retry+1)
		return
	}
	defer f.Close()

	// add newline before appending (except if file is empty)
	stat, err := f.Stat()
	if err == nil && stat.Size() > 0 {
		// file exists and has content, add newline
		_, err = f.WriteString("\n")
		if err != nil {
			fmt.Println("Error writing newline:", err)
			appendOnFile(message, retry+1)
			return
		}
	}

	// append the message to the file
	_, err = f.Write(message)
	if err != nil {
		fmt.Println("Error writing to file:", err)
		appendOnFile(message, retry+1)
		return
	}

	fmt.Println("Message appended to file:", filePath)
}
