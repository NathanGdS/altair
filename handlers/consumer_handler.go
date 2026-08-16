package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nathangds/altair/shared"
)

type registerConsumerRequest struct {
	Origin     string `json:"origin"`
	WebhookURL string `json:"webhook_url"`
}

type registerConsumerResponse struct {
	ID string `json:"id"`
}

func RegisterConsumerHandler(w http.ResponseWriter, r *http.Request) {
	var req registerConsumerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	if req.Origin == "" || req.WebhookURL == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("origin and webhook_url are required"))
		return
	}

	consumer, err := shared.Consumers.Register(req.Origin, req.WebhookURL)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	resp, _ := json.Marshal(registerConsumerResponse{ID: consumer.ID})
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

func ConsumerHeartbeatHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	err := shared.Consumers.Heartbeat(id)
	if errors.Is(err, shared.ErrConsumerNotFound) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(err.Error()))
		return
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.WriteHeader(http.StatusOK)
}

func UnregisterConsumerHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := shared.Consumers.Deactivate(id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.WriteHeader(http.StatusOK)
}
