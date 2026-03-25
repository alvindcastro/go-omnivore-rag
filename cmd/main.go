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
	"fmt"
	"log"

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

	addr := fmt.Sprintf(":%s", cfg.APIPort)
	if err := router.Run(addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
