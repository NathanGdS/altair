package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/nathangds/altair/handlers"
	"github.com/nathangds/altair/shared"
	"github.com/nathangds/altair/web"
	"github.com/nathangds/altair/workers"
)

func main() {
	defer shared.Log.Sync()

	if err := shared.InitConsumerStore("data/altair.db"); err != nil {
		shared.Log.Fatal("failed to init consumer store", zap.Error(err))
	}
	defer shared.Consumers.Close()

	http.HandleFunc("POST /publish", handlers.PublishHandler)
	http.HandleFunc("POST /consumers", handlers.RegisterConsumerHandler)
	http.HandleFunc("POST /consumers/{id}/heartbeat", handlers.ConsumerHeartbeatHandler)
	http.HandleFunc("DELETE /consumers/{id}", handlers.UnregisterConsumerHandler)
	web.RegisterWebHandlers()
	go workers.ConsumerWorker()
	go workers.DeliveryWorker()
	go workers.TTLSweeperWorker()
	go workers.PurgeMessagesWorker()
	go workers.RemoveEmptyFilesWorker("messages/processed")
	go workers.RemoveEmptyFilesWorker("messages/ready")
	// deliveries/pending is intentionally NOT wired here: DeliveryWorker renames every pending
	// file away every 5 seconds (crash-recovery rename-then-remove), so a file can vanish out
	// from under this worker's ReadDir->countLines->os.Open sequence mid-check; combined with
	// markToDelete's countLines error previously being fatal, that raced RemoveEmptyFilesWorker
	// into crashing the whole broker. Pending files are also removed on drain now (not
	// truncated), so there is nothing left for this cleanup worker to do there anyway.
	// deliveries/failed is safe: those files are terminal/static, not raced by the delivery
	// pipeline.
	go workers.RemoveEmptyFilesWorker("deliveries/failed")
	go workers.DeleteMakedFiles()

	shared.Log.Info("Server is running on port 8080")
	server := &http.Server{Addr: ":8080", Handler: nil}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			shared.Log.Fatal("Server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	shared.Log.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		shared.Log.Fatal("Server shutdown failed", zap.Error(err))
	}

	shared.Log.Info("Server gracefully stopped.")
}
