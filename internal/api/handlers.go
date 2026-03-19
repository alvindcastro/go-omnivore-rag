// internal/api/handlers.go
// Gin HTTP handlers for all API endpoints.
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go-banner-rag/config"
	"go-banner-rag/internal/azure"
	"go-banner-rag/internal/ingest"
	"go-banner-rag/internal/rag"
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

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":          "healthy",
		"index_name":      h.cfg.AzureSearchIndexName,
		"chat_model":      h.cfg.AzureOpenAIChatDeployment,
		"embedding_model": h.cfg.AzureOpenAIEmbeddingDeployment,
	})
}

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

func (h *Handler) ListChunks(c *gin.Context) {
	url := fmt.Sprintf(
		"%s/indexes/%s/docs?api-version=2024-03-01-Preview&search=*&$top=50&$select=id,filename,page_number,banner_module,banner_version",
		strings.TrimRight(h.cfg.AzureSearchEndpoint, "/"),
		h.cfg.AzureSearchIndexName,
	)

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("api-key", h.cfg.AzureSearchAPIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	var result any
	json.NewDecoder(resp.Body).Decode(&result)
	c.JSON(http.StatusOK, result)
}

// ─── RAG ──────────────────────────────────────────────────────────────────────

type askRequest struct {
	Question      string `json:"question"  binding:"required,min=5"`
	TopK          int    `json:"top_k"`
	VersionFilter string `json:"version_filter"`
	ModuleFilter  string `json:"module_filter"`
	YearFilter    string `json:"year_filter"`
}

func (h *Handler) Ask(c *gin.Context) {
	var req askRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TopK == 0 {
		req.TopK = h.cfg.TopKDefault
	}

	log.Printf("Q: %q | top_k=%d | version=%s | module=%s | year=%s",
		req.Question, req.TopK, req.VersionFilter, req.ModuleFilter, req.YearFilter)

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
	DocsPath      string `json:"docs_path"`
	Overwrite     bool   `json:"overwrite"`
	PagesPerBatch int    `json:"pages_per_batch"`
	StartPage     int    `json:"start_page"`
	EndPage       int    `json:"end_page"`
}

func (h *Handler) Ingest(c *gin.Context) {
	var req ingestRequest
	_ = c.ShouldBindJSON(&req)
	if req.DocsPath == "" {
		req.DocsPath = "data/docs"
	}
	if req.PagesPerBatch == 0 {
		req.PagesPerBatch = 10
	}

	log.Printf("Ingesting from %s (overwrite=%v, pages_per_batch=%d, start_page=%d, end_page=%d)",
		req.DocsPath, req.Overwrite, req.PagesPerBatch, req.StartPage, req.EndPage)

	result, err := ingest.Run(h.cfg, req.DocsPath, req.Overwrite, req.PagesPerBatch, req.StartPage, req.EndPage)
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
	PagesPerBatch   int    `json:"pages_per_batch"`
}

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

func (h *Handler) BlobSync(c *gin.Context) {
	if h.cfg.AzureStorageConnectionString == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "AZURE_STORAGE_CONNECTION_STRING is not configured"})
		return
	}

	var req blobSyncRequest
	_ = c.ShouldBindJSON(&req)

	if req.ContainerName == "" {
		req.ContainerName = h.cfg.AzureStorageContainerName
	}
	if req.Prefix == "" {
		req.Prefix = h.cfg.AzureStorageBlobPrefix
	}
	if req.PagesPerBatch == 0 {
		req.PagesPerBatch = 10
	}
	req.IngestAfterSync = true

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

	if req.IngestAfterSync && len(downloaded) > 0 {
		result, err := ingest.Run(h.cfg, "data/docs", req.Overwrite, req.PagesPerBatch, 0, 0)
		if err != nil {
			response["ingestion_error"] = err.Error()
		} else {
			response["ingestion"] = result
		}
	}

	response["message"] = "Sync complete"
	c.JSON(http.StatusOK, response)
}

// ─── Summarizer ───────────────────────────────────────────────────────────────

func (h *Handler) SummarizeChanges(c *gin.Context) {
	h.handleSummarize(c, "changes")
}

func (h *Handler) SummarizeBreaking(c *gin.Context) {
	h.handleSummarize(c, "breaking")
}

func (h *Handler) SummarizeActions(c *gin.Context) {
	h.handleSummarize(c, "actions")
}

func (h *Handler) SummarizeCompatibility(c *gin.Context) {
	h.handleSummarize(c, "compatibility")
}

func (h *Handler) SummarizeFull(c *gin.Context) {
	var req rag.SummarizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	summarizer := rag.NewSummarizer(h.openai, h.search)
	result, err := summarizer.SummarizeFull(req)
	if err != nil {
		log.Printf("SummarizeFull error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) handleSummarize(c *gin.Context, topic string) {
	var req rag.SummarizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	summarizer := rag.NewSummarizer(h.openai, h.search)
	result, err := summarizer.SummarizeTopic(req, topic)
	if err != nil {
		log.Printf("Summarize[%s] error: %v", topic, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}
