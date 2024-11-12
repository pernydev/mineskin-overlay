package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/joho/godotenv"
	handler "github.com/pernydev/mineskin-overlay/api"
)

func main() {
	godotenv.Load()
	// Map the /api endpoint to our handler
	http.HandleFunc("/api", handler.Handler)

	port := "8080"
	fmt.Printf("Server starting on http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
