// internal/api/router.go
// Wires all routes to their handlers.
package api

import (
	_ "go-omnivore-rag/docs"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"go-omnivore-rag/config"

	"github.com/gin-gonic/gin"
)

// NewRouter creates and returns a configured Gin router.
func NewRouter(cfg *config.Config) *gin.Engine {
	router := gin.Default()

	h := NewHandler(cfg)

	// ── Swagger UI ────────────────────────────────────────────────────────────
	router.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

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

		// ── Banner / Module-scoped ask ────────────────────────────────────────
		// Handles /banner/:module/ask for any Banner module (finance, hr, etc.).
		// Static sub-routes below (student, blob, summarize) take precedence
		// over the :module parameter in Gin's router tree.
		banner.POST("/:module/ask", h.ModuleAsk)

		// ── Banner / Student ──────────────────────────────────────────────────
		student := banner.Group("/student")
		{
			student.POST("/ask", h.StudentAsk)
			student.POST("/ingest", h.StudentIngest)
			student.POST("/procedure", h.StudentProcedure)
			student.POST("/lookup", h.StudentLookup)
			student.POST("/cross-reference", h.StudentCrossReference)
		}

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
