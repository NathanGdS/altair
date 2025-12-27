package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nathangds/altair/handlers"
	"github.com/nathangds/altair/web"
	"github.com/nathangds/altair/workers"
)

func main() {
	http.HandleFunc("POST /publish", handlers.PublishHandler)
	web.RegisterWebHandlers()
	go workers.ConsumerWorker()
	go workers.PurgeMessagesWorker()
	go workers.RemoveEmptyFilesWorker("messages/processed")
	go workers.RemoveEmptyFilesWorker("messages/ready")
	go workers.DeleteMakedFiles()

	fmt.Println("Server is running on port 8080")
	server := &http.Server{Addr: ":8080", Handler: nil}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Set up a channel to listen for OS signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Block until a signal is received
	<-quit
	log.Println("Shutting down server...")

	// Create a context with a timeout for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempt to gracefully shut down the server
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	log.Println("Server gracefully stopped.")
}
