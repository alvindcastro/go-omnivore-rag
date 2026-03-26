// internal/api/handlers.go
// Gin HTTP handlers for all API endpoints.
//
//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/main.go -d ../../ -o ../../docs/
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go-omnivore-rag/config"
	"go-omnivore-rag/internal/azure"
	"go-omnivore-rag/internal/ingest"
	"go-omnivore-rag/internal/rag"
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
//
//	@Summary	Service health check
//	@Tags		system
//	@Produce	json
//	@Success	200	{object}	map[string]string
//	@Router		/health [get]
func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":          "healthy",
		"index_name":      h.cfg.AzureSearchIndexName,
		"chat_model":      h.cfg.AzureOpenAIChatDeployment,
		"embedding_model": h.cfg.AzureOpenAIEmbeddingDeployment,
	})
}

// IndexStats godoc
//
//	@Summary	Azure Search index statistics
//	@Tags		system
//	@Produce	json
//	@Success	200	{object}	map[string]any
//	@Failure	500	{object}	map[string]string
//	@Router		/index/stats [get]
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

// CreateIndex godoc
//
//	@Summary	Create the Azure Search index
//	@Tags		system
//	@Produce	json
//	@Success	200	{object}	map[string]string
//	@Failure	500	{object}	map[string]string
//	@Router		/index/create [post]
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

// ListChunks godoc
//
//	@Summary	List up to 50 indexed document chunks
//	@Tags		system
//	@Produce	json
//	@Success	200	{object}	any
//	@Failure	500	{object}	map[string]string
//	@Router		/debug/chunks [get]
func (h *Handler) ListChunks(c *gin.Context) {
	url := fmt.Sprintf(
		"%s/indexes/%s/docs?api-version=2024-03-01-Preview&search=*&$top=50&$select=id,filename,page_number,banner_module,banner_version,source_type,sop_number",
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

// ─── Banner ───────────────────────────────────────────────────────────────────

type bannerAskRequest struct {
	Question      string `json:"question"       binding:"required,min=5"`
	TopK          int    `json:"top_k"`
	VersionFilter string `json:"version_filter"`
	ModuleFilter  string `json:"module_filter"`
	YearFilter    string `json:"year_filter"`
}

// BannerAsk godoc
//
//	@Summary	Ask a question against Banner release notes
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		body	body		bannerAskRequest	true	"Question payload"
//	@Success	200		{object}	rag.AskResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/ask [post]
func (h *Handler) BannerAsk(c *gin.Context) {
	var req bannerAskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TopK == 0 {
		req.TopK = h.cfg.TopKDefault
	}

	log.Printf("[banner/ask] Q: %q | top_k=%d | version=%s | module=%s | year=%s",
		req.Question, req.TopK, req.VersionFilter, req.ModuleFilter, req.YearFilter)

	resp, err := h.pipeline.Ask(rag.AskRequest{
		Question:         req.Question,
		TopK:             req.TopK,
		VersionFilter:    req.VersionFilter,
		ModuleFilter:     req.ModuleFilter,
		YearFilter:       req.YearFilter,
		SourceTypeFilter: "banner",
	})
	if err != nil {
		log.Printf("[banner/ask] error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[banner/ask] A: %d chars | %d sources", len(resp.Answer), resp.RetrievalCount)
	c.JSON(http.StatusOK, resp)
}

type bannerIngestRequest struct {
	Overwrite     bool   `json:"overwrite"`
	DocsPath      string `json:"docs_path"`
	PagesPerBatch int    `json:"pages_per_batch"`
	StartPage     int    `json:"start_page"`
	EndPage       int    `json:"end_page"`
}

// BannerIngest godoc
//
//	@Summary	Ingest Banner release note PDFs into the search index
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		body	body		bannerIngestRequest	false	"Ingest options"
//	@Success	200		{object}	map[string]any
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/ingest [post]
func (h *Handler) BannerIngest(c *gin.Context) {
	var req bannerIngestRequest
	_ = c.ShouldBindJSON(&req)
	if req.DocsPath == "" {
		req.DocsPath = "data/docs/banner"
	}
	if req.PagesPerBatch == 0 {
		req.PagesPerBatch = 10
	}

	log.Printf("[banner/ingest] path=%s overwrite=%v pages_per_batch=%d start=%d end=%d",
		req.DocsPath, req.Overwrite, req.PagesPerBatch, req.StartPage, req.EndPage)

	result, err := ingest.Run(h.cfg, req.DocsPath, req.Overwrite, req.PagesPerBatch, req.StartPage, req.EndPage)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ─── Banner — Blob Storage ────────────────────────────────────────────────────

type blobSyncRequest struct {
	ContainerName   string `json:"container_name"`
	Prefix          string `json:"prefix"`
	Overwrite       bool   `json:"overwrite"`
	IngestAfterSync bool   `json:"ingest_after_sync"`
	PagesPerBatch   int    `json:"pages_per_batch"`
}

// BlobList godoc
//
//	@Summary	List documents in Azure Blob Storage
//	@Tags		banner
//	@Produce	json
//	@Param		prefix	query		string	false	"Blob name prefix filter"
//	@Success	200		{object}	map[string]any
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/blob/list [get]
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
//
//	@Summary	Download blobs from Azure Storage and ingest them
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		body	body		blobSyncRequest	false	"Sync options"
//	@Success	200		{object}	map[string]any
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/blob/sync [post]
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

	log.Printf("[banner/blob/sync] container=%q prefix=%q", req.ContainerName, req.Prefix)
	downloaded, err := blobClient.DownloadDocuments(req.Prefix, "data/docs/banner", req.Overwrite)
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
		result, err := ingest.Run(h.cfg, "data/docs/banner", req.Overwrite, req.PagesPerBatch, 0, 0)
		if err != nil {
			response["ingestion_error"] = err.Error()
		} else {
			response["ingestion"] = result
		}
	}

	response["message"] = "Sync complete"
	c.JSON(http.StatusOK, response)
}

// ─── Banner — Summarizer ──────────────────────────────────────────────────────

// SummarizeChanges godoc
//
//	@Summary	Summarize what changed in a Banner release
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		body	body		rag.SummarizeRequest	true	"Summarize request"
//	@Success	200		{object}	rag.SummarizeResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/summarize/changes [post]
func (h *Handler) SummarizeChanges(c *gin.Context) {
	h.handleSummarize(c, "changes")
}

// SummarizeBreaking godoc
//
//	@Summary	Summarize breaking changes in a Banner release
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		body	body		rag.SummarizeRequest	true	"Summarize request"
//	@Success	200		{object}	rag.SummarizeResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/summarize/breaking [post]
func (h *Handler) SummarizeBreaking(c *gin.Context) {
	h.handleSummarize(c, "breaking")
}

// SummarizeActions godoc
//
//	@Summary	Summarize required actions for a Banner release
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		body	body		rag.SummarizeRequest	true	"Summarize request"
//	@Success	200		{object}	rag.SummarizeResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/summarize/actions [post]
func (h *Handler) SummarizeActions(c *gin.Context) {
	h.handleSummarize(c, "actions")
}

// SummarizeCompatibility godoc
//
//	@Summary	Summarize compatibility notes for a Banner release
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		body	body		rag.SummarizeRequest	true	"Summarize request"
//	@Success	200		{object}	rag.SummarizeResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/summarize/compatibility [post]
func (h *Handler) SummarizeCompatibility(c *gin.Context) {
	h.handleSummarize(c, "compatibility")
}

// SummarizeFull godoc
//
//	@Summary	Full summary across all topics for a Banner release
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		body	body		rag.SummarizeRequest	true	"Summarize request"
//	@Success	200		{object}	rag.FullSummaryResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/summarize/full [post]
func (h *Handler) SummarizeFull(c *gin.Context) {
	var req rag.SummarizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	summarizer := rag.NewSummarizer(h.openai, h.search)
	result, err := summarizer.SummarizeFull(req)
	if err != nil {
		log.Printf("[banner/summarize/full] error: %v", err)
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
		log.Printf("[banner/summarize/%s] error: %v", topic, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ─── SOP ──────────────────────────────────────────────────────────────────────

type sopAskRequest struct {
	Question string `json:"question" binding:"required,min=5"`
	TopK     int    `json:"top_k"`
}

// SopAsk godoc
//
//	@Summary	Ask a question against SOPs
//	@Tags		sop
//	@Accept		json
//	@Produce	json
//	@Param		body	body		sopAskRequest	true	"Question payload"
//	@Success	200		{object}	rag.AskResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/sop/ask [post]
func (h *Handler) SopAsk(c *gin.Context) {
	var req sopAskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TopK == 0 {
		req.TopK = h.cfg.TopKDefault
	}

	log.Printf("[sop/ask] Q: %q | top_k=%d", req.Question, req.TopK)

	resp, err := h.pipeline.Ask(rag.AskRequest{
		Question:         req.Question,
		TopK:             req.TopK,
		SourceTypeFilter: "sop",
	})
	if err != nil {
		log.Printf("[sop/ask] error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[sop/ask] A: %d chars | %d sources", len(resp.Answer), resp.RetrievalCount)
	c.JSON(http.StatusOK, resp)
}

type sopIngestRequest struct {
	Overwrite bool `json:"overwrite"`
}

// SopIngest godoc
//
//	@Summary	Ingest SOP documents into the search index
//	@Tags		sop
//	@Accept		json
//	@Produce	json
//	@Param		body	body		sopIngestRequest	false	"Ingest options"
//	@Success	200		{object}	map[string]any
//	@Failure	500		{object}	map[string]string
//	@Router		/sop/ingest [post]
func (h *Handler) SopIngest(c *gin.Context) {
	var req sopIngestRequest
	_ = c.ShouldBindJSON(&req)

	const sopPath = "data/docs/sop"
	log.Printf("[sop/ingest] path=%s overwrite=%v", sopPath, req.Overwrite)

	result, err := ingest.Run(h.cfg, sopPath, req.Overwrite, 0, 0, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// SopList godoc
//
//	@Summary	List all ingested SOPs
//	@Tags		sop
//	@Produce	json
//	@Success	200	{object}	map[string]any
//	@Failure	500	{object}	map[string]string
//	@Router		/sop [get]
func (h *Handler) SopList(c *gin.Context) {
	entries, err := h.search.ListSOPs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"count": len(entries),
		"sops":  entries,
	})
}
