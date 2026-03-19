// internal/api/handlers.go
// Gin HTTP handlers for all API endpoints.
// Each handler is thin — it validates input, calls the right service, and returns JSON.
package api

import (
	"log"
	"net/http"

	"go-banner-rag/config"
	"go-banner-rag/internal/azure"
	"go-banner-rag/internal/ingest"
	"go-banner-rag/internal/rag"

	"github.com/gin-gonic/gin"
)

// Handler holds shared dependencies injected at startup.
type Handler struct {
	cfg      *config.Config
	openai   *azure.OpenAIClient
	search   *azure.SearchClient
	pipeline *rag.Pipeline
}

// NewHandler creates a Handler with all dependencies wired up.
func NewHandler(cfg *config.Config) *Handler {
	openai := azure.NewOpenAIClient(cfg)
	search := azure.NewSearchClient(cfg)
	return &Handler{
		cfg:      cfg,
		openai:   openai,
		search:   search,
		pipeline: rag.NewPipeline(openai, search),
	}
}

// ─── System ───────────────────────────────────────────────────────────────────

// Health godoc
// @Summary Health check
// @Tags System
// @Success 200
// @Router /health [get]
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":          "healthy",
		"index_name":      h.cfg.AzureSearchIndexName,
		"chat_model":      h.cfg.AzureOpenAIChatDeployment,
		"embedding_model": h.cfg.AzureOpenAIEmbeddingDeployment,
	})
}

// IndexStats godoc
// @Summary Azure AI Search index stats
// @Tags System
// @Router /index/stats [get]
func (h *Handler) IndexStats(c *gin.Context) {
	count, err := h.search.GetDocumentCount()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"index_name":     h.cfg.AzureSearchIndexName,
		"document_count": count,
		"status":         "ready",
	})
}

// ─── RAG ──────────────────────────────────────────────────────────────────────

type askRequest struct {
	Question      string `json:"question"       binding:"required,min=5"`
	TopK          int    `json:"top_k"`
	VersionFilter string `json:"version_filter"`
	ModuleFilter  string `json:"module_filter"`
	YearFilter    string `json:"year_filter"`
}

// Ask godoc
// @Summary Ask a Banner upgrade question
// @Tags RAG
// @Accept json
// @Produce json
// @Router /ask [post]
func (h *Handler) Ask(c *gin.Context) {
	var req askRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TopK == 0 {
		req.TopK = h.cfg.TopKDefault
	}

	log.Printf("Q: %q | top_k=%d | version=%s | module=%s",
		req.Question, req.TopK, req.VersionFilter, req.ModuleFilter)

	resp, err := h.pipeline.Ask(rag.AskRequest{
		Question:      req.Question,
		TopK:          req.TopK,
		VersionFilter: req.VersionFilter,
		ModuleFilter:  req.ModuleFilter,
		YearFilter:    req.YearFilter,
	})
	if err != nil {
		log.Printf("RAG error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("A: %d chars | %d sources", len(resp.Answer), resp.RetrievalCount)
	c.JSON(http.StatusOK, resp)
}

// ─── Ingestion ────────────────────────────────────────────────────────────────

type ingestRequest struct {
	DocsPath  string `json:"docs_path"`
	Overwrite bool   `json:"overwrite"`
}

// Ingest godoc
// @Summary Ingest Banner documents from local folder
// @Tags Ingestion
// @Router /ingest [post]
func (h *Handler) Ingest(c *gin.Context) {
	var req ingestRequest
	_ = c.ShouldBindJSON(&req)
	if req.DocsPath == "" {
		req.DocsPath = "data/docs"
	}

	log.Printf("Ingesting from %s (overwrite=%v)", req.DocsPath, req.Overwrite)

	result, err := ingest.Run(h.cfg, req.DocsPath, req.Overwrite)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ─── Blob Storage ─────────────────────────────────────────────────────────────

type blobSyncRequest struct {
	ContainerName   string `json:"container_name"`
	Prefix          string `json:"prefix"`
	Overwrite       bool   `json:"overwrite"`
	IngestAfterSync bool   `json:"ingest_after_sync"`
}

// BlobList godoc
// @Summary List documents in Azure Blob Storage
// @Tags Blob Storage
// @Router /blob/list [get]
func (h *Handler) BlobList(c *gin.Context) {
	if h.cfg.AzureStorageConnectionString == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "AZURE_STORAGE_CONNECTION_STRING is not configured"})
		return
	}

	blob, err := azure.NewBlobClient(h.cfg.AzureStorageConnectionString, h.cfg.AzureStorageContainerName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	prefix := c.Query("prefix")
	docs, err := blob.ListDocuments(prefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"container_name": h.cfg.AzureStorageContainerName,
		"document_count": len(docs),
		"documents":      docs,
	})
}

// BlobSync godoc
// @Summary Download PDFs from Azure Blob and ingest them
// @Tags Blob Storage
// @Router /blob/sync [post]
func (h *Handler) BlobSync(c *gin.Context) {
	if h.cfg.AzureStorageConnectionString == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "AZURE_STORAGE_CONNECTION_STRING is not configured"})
		return
	}

	var req blobSyncRequest
	_ = c.ShouldBindJSON(&req)

	// Defaults
	if req.ContainerName == "" {
		req.ContainerName = h.cfg.AzureStorageContainerName
	}
	if req.Prefix == "" {
		req.Prefix = h.cfg.AzureStorageBlobPrefix
	}
	req.IngestAfterSync = true // default on

	blobClient, err := azure.NewBlobClient(h.cfg.AzureStorageConnectionString, req.ContainerName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("Syncing blobs from container=%q prefix=%q", req.ContainerName, req.Prefix)
	downloaded, err := blobClient.DownloadDocuments(req.Prefix, "data/docs", req.Overwrite)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := gin.H{
		"status":           "success",
		"files_downloaded": len(downloaded),
		"downloaded_paths": downloaded,
	}

	// Optionally ingest right after download
	if req.IngestAfterSync && len(downloaded) > 0 {
		result, err := ingest.Run(h.cfg, "data/docs", req.Overwrite)
		if err != nil {
			response["ingestion_error"] = err.Error()
		} else {
			response["ingestion"] = result
		}
	}

	response["message"] = "Sync complete"
	c.JSON(http.StatusOK, response)
}

// CreateIndex godoc
// @Summary Create or recreate the Azure AI Search index
// @Tags System
// @Router /index/create [post]
func (h *Handler) CreateIndex(c *gin.Context) {
	if err := h.search.CreateIndex(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":     "created",
		"index_name": h.cfg.AzureSearchIndexName,
	})
}
