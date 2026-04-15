// internal/api/router.go
// Wires all routes to their handlers.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	_ "go-omnivore-rag/docs"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"go-omnivore-rag/config"

	"github.com/gin-gonic/gin"
)

// NewRouter creates and returns a configured Gin router.
func NewRouter(cfg *config.Config) *gin.Engine {
	router := gin.Default()

	router.Use(requestIDMiddleware())
	router.Use(corsMiddleware())
	if cfg.APIKey != "" {
		router.Use(apiKeyMiddleware(cfg.APIKey))
	}

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

// requestIDMiddleware attaches a unique X-Request-ID to every request and response.
// Callers (n8n, LangGraph) can set their own X-Request-ID; the middleware echoes it
// back and propagates it so every log line for a request shares the same ID.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

// corsMiddleware adds CORS headers so browser-based n8n cloud and other web clients
// can call the API without being blocked by the same-origin policy.
// Allowed origins can be tightened via the CORS_ALLOWED_ORIGINS env var in future;
// for now a permissive default is safe on a private network.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-Request-ID")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// apiKeyMiddleware enforces Bearer token authentication on all endpoints except
// /health and /docs (which must remain open for liveness probes and Swagger UI).
// To enable: set API_KEY in .env. When API_KEY is empty the middleware is not registered.
func apiKeyMiddleware(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		// Public paths — no auth required.
		if path == "/health" || strings.HasPrefix(path, "/docs") {
			c.Next()
			return
		}

		header := c.GetHeader("Authorization")
		token, found := strings.CutPrefix(header, "Bearer ")
		if !found || strings.TrimSpace(token) != apiKey {
			reqID, _ := c.Get("request_id")
			slog.Warn("unauthorized request",
				"path", path,
				"method", c.Request.Method,
				"request_id", reqID,
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or missing API key"})
			return
		}

		c.Next()
	}
}
