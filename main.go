package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/nathangds/altair/handlers"
)

func main() {
	http.HandleFunc("POST /publish", handlers.PublishHandler)

	fmt.Println("Server is running on port 8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
