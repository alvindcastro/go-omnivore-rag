// internal/api/router.go
// Wires all routes to their handlers.
package api

import (
	"go-banner-rag/config"

	"github.com/gin-gonic/gin"
)

// NewRouter creates and returns a configured Gin router.
func NewRouter(cfg *config.Config) *gin.Engine {
	router := gin.Default()

	h := NewHandler(cfg)

	// System
	router.GET("/health", h.Health)
	router.GET("/index/stats", h.IndexStats)
	router.POST("/index/create", h.CreateIndex)

	// RAG
	router.POST("/ask", h.Ask)

	// Ingestion
	router.POST("/ingest", h.Ingest)

	// Blob Storage
	router.GET("/blob/list", h.BlobList)
	router.POST("/blob/sync", h.BlobSync)

	router.GET("/debug/chunks", h.ListChunks)

	return router
}
