// cmd/main.go
// Application entry point.
// Loads config, sets up the router, starts the HTTP server.

//	@title			go-omnivore-rag API
//	@version		1.0
//	@description	RAG pipeline over Banner release notes and Standard Operating Procedures (SOPs), backed by Azure OpenAI and Azure Cognitive Search.
//	@host			localhost:8000
//	@BasePath		/

package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-omnivore-rag/config"
	"go-omnivore-rag/internal/api"
)

func main() {
	log.Println("🚀 Starting Banner Upgrade RAG API...")

	// Load all settings from .env
	cfg := config.Load()

	log.Printf("   Chat model      : %s", cfg.AzureOpenAIChatDeployment)
	log.Printf("   Embedding model : %s", cfg.AzureOpenAIEmbeddingDeployment)
	log.Printf("   Search index    : %s", cfg.AzureSearchIndexName)
	log.Printf("   Blob container  : %s", cfg.AzureStorageContainerName)
	log.Printf("   Listening on    : :%s", cfg.APIPort)

	router := api.NewRouter(cfg)

	srv := &http.Server{
		Addr:    ":" + cfg.APIPort,
		Handler: router,
	}

	// Start server in a goroutine so the main goroutine can listen for signals.
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Block until SIGTERM (sent by ACA/Docker on shutdown) or SIGINT (Ctrl-C).
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	// Give in-flight requests up to 30 seconds to complete before hard-stopping.
	// ACA waits 30 s after SIGTERM before sending SIGKILL, so these match.
	log.Println("Shutting down — draining in-flight requests (30s max)...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Forced shutdown after timeout: %v", err)
	}
	log.Println("Server stopped.")
}
