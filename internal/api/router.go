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

	// ── System ────────────────────────────────────────────────────────────────
	router.GET("/health", h.Health)
	router.GET("/index/stats", h.IndexStats)
	router.POST("/index/create", h.CreateIndex)
	router.GET("/debug/chunks", h.ListChunks)

	// ── Banner ────────────────────────────────────────────────────────────────
	banner := router.Group("/banner")
	{
		banner.POST("/ask", h.BannerAsk)
		banner.POST("/ingest", h.BannerIngest)

		blob := banner.Group("/blob")
		{
			blob.GET("/list", h.BlobList)
			blob.POST("/sync", h.BlobSync)
		}

		summarize := banner.Group("/summarize")
		{
			summarize.POST("/changes", h.SummarizeChanges)
			summarize.POST("/breaking", h.SummarizeBreaking)
			summarize.POST("/actions", h.SummarizeActions)
			summarize.POST("/compatibility", h.SummarizeCompatibility)
			summarize.POST("/full", h.SummarizeFull)
		}
	}

	// ── SOP ───────────────────────────────────────────────────────────────────
	sop := router.Group("/sop")
	{
		sop.GET("", h.SopList)
		sop.POST("/ask", h.SopAsk)
		sop.POST("/ingest", h.SopIngest)
	}

	return router
}
