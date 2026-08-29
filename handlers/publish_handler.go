package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nathangds/altair/shared"
)

type Message struct {
	Origin     string         `json:"origin"`
	Id         string         `json:"id"`
	Data       map[string]any `json:"data"`
	ReceivedAt time.Time      `json:"received_at"`
	StressTest bool           `json:"stress-test"`
}

func (m *Message) Instantiate() error {
	if m.Origin == "" && m.StressTest {
		m.Origin = "stress-test"
	} else if m.Origin == "" {
		return errors.New("Origin is required")
	}

	// Origin flows downstream into workers/delivery.go's pendingFilePath/failedFilePath,
	// which interpolate it directly into a filesystem path. Sanitize it here, at the
	// /publish entry point, so a path-traversal origin (e.g. "../../evil") can never reach
	// those sinks.
	sanitized, err := shared.SanitizeOrigin(m.Origin)
	if err != nil {
		return err
	}
	m.Origin = sanitized

	m.Id = uuid.New().String()
	m.ReceivedAt = time.Now().UTC()
	return nil
}

type writePacket struct {
	Message []byte
	Origin  string
}

var fileWriterChan chan writePacket

func init() {
	fileWriterChan = make(chan writePacket, 200000)
	go fileWriterWorker()
}

func fileWriterWorker() {
	for pkt := range fileWriterChan {
		writeToFile(pkt.Message, pkt.Origin)
	}
}

func writeToFile(message []byte, origin string) {
	filePath := getFilePath(origin)

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		shared.Log.Error("Error opening file: " + err.Error())
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err == nil && stat.Size() > 0 {
		_, err = f.WriteString("\n")
		if err != nil {
			shared.Log.Error("Error writing newline:" + err.Error())
			return
		}
	}

	_, err = f.Write(message)
	if err != nil {
		shared.Log.Error("Error writing to file: " + err.Error())
		return
	}

	shared.Log.Info("Message appended to: " + filePath)
}

func PublishHandler(w http.ResponseWriter, r *http.Request) {
	message := Message{}

	json.NewDecoder(r.Body).Decode(&message)

	err := message.Instantiate()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	jsonBytes, err := json.Marshal(message)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	// send to async worker
	fileWriterChan <- writePacket{
		Message: jsonBytes,
		Origin:  message.Origin,
	}

	w.WriteHeader(http.StatusOK)
	w.Write(jsonBytes)
}

func getFilePath(origin string) string {
	fileName := time.Now().Format("2006-01-02")
	return fmt.Sprintf("messages/ready/%s-%s.json", origin, fileName)
}
