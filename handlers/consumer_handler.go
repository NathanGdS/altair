package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

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

	origin, err := shared.SanitizeOrigin(req.Origin)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	if err := validateWebhookURL(req.WebhookURL); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}

	consumer, err := shared.Consumers.Register(origin, req.WebhookURL)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	resp, _ := json.Marshal(registerConsumerResponse{ID: consumer.ID})
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

// validateWebhookURL rejects anything that isn't a structurally sane http(s) URL. This is
// registered on an unauthenticated endpoint (POST /consumers), so it closes both:
//   - an SSRF-adjacent gap: without this, a caller could register a non-http(s) URL scheme
//     (or one with no host at all) as a delivery target.
//   - a resource-exhaustion gap: a structurally invalid webhook_url currently fails inside
//     http.NewRequest in workers/delivery_worker.go, burning all delivery retry attempts
//     plus backoff sleep per message, forever, since nothing ever rejects it up front.
func validateWebhookURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("webhook_url is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("webhook_url must use the http or https scheme")
	}
	if parsed.Host == "" {
		return errors.New("webhook_url must include a host")
	}
	return nil
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
