package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/shared"
)

func newMux() (*http.ServeMux, *atomic.Int64) {
	var received atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		resp, _ := json.Marshal(map[string]int64{"received": received.Load()})
		w.Header().Set("Content-Type", "application/json")
		w.Write(resp)
	})

	return mux, &received
}

func register(altairURL, origin, webhookURL string) (string, error) {
	body, _ := json.Marshal(map[string]string{"origin": origin, "webhook_url": webhookURL})

	resp, err := http.Post(altairURL+"/consumers", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.ID, nil
}

func unregister(altairURL, consumerID string) error {
	req, err := http.NewRequest(http.MethodDelete, altairURL+"/consumers/"+consumerID, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func heartbeatLoop(ctx context.Context, altairURL, consumerID string) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req, _ := http.NewRequest(http.MethodPost, altairURL+"/consumers/"+consumerID+"/heartbeat", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				shared.Log.Error("heartbeat failed", zap.Error(err))
				continue
			}
			resp.Body.Close()
		}
	}
}

func main() {
	altairURL := flag.String("altair-url", "http://localhost:8080", "base URL of the Altair broker")
	origin := flag.String("origin", "examples-consumer", "origin to subscribe to")
	port := flag.String("port", "9090", "port this consumer listens on for webhook deliveries")
	flag.Parse()

	listener, err := net.Listen("tcp", ":"+*port)
	if err != nil {
		shared.Log.Fatal("failed to bind port", zap.Error(err))
	}

	mux, _ := newMux()
	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			shared.Log.Fatal("consumer server error", zap.Error(err))
		}
	}()
	shared.Log.Info("consumer listening", zap.String("port", *port))

	webhookURL := "http://localhost:" + *port + "/webhook"
	consumerID, err := register(*altairURL, *origin, webhookURL)
	if err != nil {
		shared.Log.Fatal("failed to register consumer", zap.Error(err))
	}
	shared.Log.Info("registered consumer", zap.String("id", consumerID), zap.String("origin", *origin))

	heartbeatCtx, stopHeartbeat := context.WithCancel(context.Background())
	go heartbeatLoop(heartbeatCtx, *altairURL, consumerID)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shared.Log.Info("shutting down consumer...")
	stopHeartbeat()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)

	_ = unregister(*altairURL, consumerID)
	shared.Log.Info("consumer stopped")
}
