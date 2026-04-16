// cmd/server/main.go
// Entry point for the Ask Banner adapter service.
// Reads RAG_BACKEND_URL from environment, wires the adapter client,
// and starts the ChatHandler on PORT (default 8080).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-omnivore-rag/api"
	"go-omnivore-rag/internal/adapter"
)

func main() {
	ragBackendURL := os.Getenv("RAG_BACKEND_URL")
	if ragBackendURL == "" {
		log.Fatal("RAG_BACKEND_URL environment variable is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	client := adapter.NewAdapterClient(ragBackendURL)
	handler := api.NewChatHandler(client)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  35 * time.Second, // slightly above adapter 30 s client timeout
		WriteTimeout: 40 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Ask Banner adapter starting — port %s → backend %s", port, ragBackendURL)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Println("Shutting down adapter (15 s drain)...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("forced shutdown: %v", err)
	}
	log.Println("Adapter stopped.")
}
