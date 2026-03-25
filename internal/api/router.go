// internal/api/router.go
// Wires all routes to their handlers.
package api

import (
	"github.com/gin-gonic/gin"
	"go-omnivore-rag/config"
)

// NewRouter creates and returns a configured Gin router.
func NewRouter(cfg *config.Config) *gin.Engine {
	router := gin.Default()

	h := NewHandler(cfg)

	// System
	router.GET("/health", h.Health)
	router.GET("/index/stats", h.IndexStats)
	router.POST("/index/create", h.CreateIndex)

	// Debug
	router.GET("/debug/chunks", h.ListChunks)

	// RAG
	router.POST("/ask", h.Ask)

	// Ingestion
	router.POST("/ingest", h.Ingest)

	// Blob Storage
	router.GET("/blob/list", h.BlobList)
	router.POST("/blob/sync", h.BlobSync)

	// Summarizer
	router.POST("/summarize/changes", h.SummarizeChanges)
	router.POST("/summarize/breaking", h.SummarizeBreaking)
	router.POST("/summarize/actions", h.SummarizeActions)
	router.POST("/summarize/compatibility", h.SummarizeCompatibility)
	router.POST("/summarize/full", h.SummarizeFull)

	return router
}
