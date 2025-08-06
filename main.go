package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

func main() {
	http.HandleFunc("POST /publish", PublishHandler)

	fmt.Println("Server is running on port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
