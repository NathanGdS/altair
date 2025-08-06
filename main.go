package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/nathangds/altair/handlers"
	"github.com/nathangds/altair/workers"
)

func main() {
	http.HandleFunc("POST /publish", handlers.PublishHandler)

	go workers.ConsumerWorker()

	fmt.Println("Server is running on port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
