// internal/api/handlers.go
// Gin HTTP handlers for all API endpoints.
//
//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/main.go -d ../../ -o ../../docs/
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go-omnivore-rag/config"
	"go-omnivore-rag/internal/azure"
	"go-omnivore-rag/internal/ingest"
	"go-omnivore-rag/internal/rag"
	"go-omnivore-rag/internal/websearch"

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
	tavily := websearch.NewTavilyClient(cfg.TavilyAPIKey) // nil when TAVILY_API_KEY is unset
	return &Handler{
		cfg:      cfg,
		openai:   openai,
		search:   search,
		pipeline: rag.NewPipeline(openai, search, tavily, cfg.ConfidenceHighThreshold, cfg.ConfidenceLowThreshold),
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
	Mode          string `json:"mode"` // "local" (default), "web", "hybrid", "auto"
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

	reqID, _ := c.Get("request_id")
	slog.Info("banner/ask",
		"question", req.Question, "top_k", req.TopK,
		"version", req.VersionFilter, "module", req.ModuleFilter, "year", req.YearFilter,
		"request_id", reqID,
	)

	resp, err := h.pipeline.Ask(rag.AskRequest{
		Question:         req.Question,
		TopK:             req.TopK,
		VersionFilter:    req.VersionFilter,
		ModuleFilter:     req.ModuleFilter,
		YearFilter:       req.YearFilter,
		SourceTypeFilter: azure.SourceTypeBanner,
		Mode:             req.Mode,
	})
	if err != nil {
		slog.Error("banner/ask failed", "error", err, "request_id", reqID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/ask done",
		"answer_len", len(resp.Answer), "sources", resp.RetrievalCount,
		"top_score", resp.TopScore, "request_id", reqID,
	)
	c.JSON(http.StatusOK, resp)
}

// moduleAskRequest is the body for the module-scoped ask endpoint.
// The Banner module is taken from the URL path (:module), not this body.
type moduleAskRequest struct {
	Question      string `json:"question"       binding:"required,min=5"`
	TopK          int    `json:"top_k"`
	VersionFilter string `json:"version_filter"`
	YearFilter    string `json:"year_filter"`
	SectionFilter string `json:"section_filter"`
	SourceType    string `json:"source_type"` // optional — narrows to a specific doc type
	Mode          string `json:"mode"`        // "local" (default), "web", "hybrid", "auto"
}

// ModuleAsk godoc
//
//	@Summary	Ask a question scoped to a specific Banner module
//	@Description	Module is taken from the URL path (e.g. /banner/finance/ask). Supported values: finance, student, hr, financial-aid, ar, general.
//	@Tags		banner
//	@Accept		json
//	@Produce	json
//	@Param		module	path		string				true	"Banner module name"
//	@Param		body	body		moduleAskRequest	true	"Question payload"
//	@Success	200		{object}	rag.AskResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/{module}/ask [post]
func (h *Handler) ModuleAsk(c *gin.Context) {
	module := c.Param("module")

	var req moduleAskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TopK == 0 {
		req.TopK = h.cfg.TopKDefault
	}

	reqID, _ := c.Get("request_id")
	slog.Info("banner/module/ask",
		"module", module, "question", req.Question, "top_k", req.TopK,
		"mode", req.Mode, "version", req.VersionFilter,
		"request_id", reqID,
	)

	resp, err := h.pipeline.Ask(rag.AskRequest{
		Question:         req.Question,
		TopK:             req.TopK,
		ModuleFilter:     module,
		VersionFilter:    req.VersionFilter,
		YearFilter:       req.YearFilter,
		SectionFilter:    req.SectionFilter,
		SourceTypeFilter: req.SourceType,
		Mode:             req.Mode,
	})
	if err != nil {
		slog.Error("banner/module/ask failed", "module", module, "error", err, "request_id", reqID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/module/ask done",
		"module", module, "answer_len", len(resp.Answer),
		"sources", resp.RetrievalCount, "routing", routingMode(resp),
		"top_score", resp.TopScore, "request_id", reqID,
	)
	c.JSON(http.StatusOK, resp)
}

// routingMode returns the effective mode string for logging, including auto-routing decisions.
func routingMode(resp *rag.AskResponse) string {
	if resp.Routing != nil {
		return "auto→" + resp.Routing.ModeUsed
	}
	return "explicit"
}

type ingestRequest struct {
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
//	@Param		body	body		ingestRequest	false	"Ingest options"
//	@Success	200		{object}	map[string]any
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/ingest [post]
func (h *Handler) BannerIngest(c *gin.Context) {
	var req ingestRequest
	_ = c.ShouldBindJSON(&req)
	if req.DocsPath == "" {
		req.DocsPath = "data/docs/banner"
	}
	if req.PagesPerBatch == 0 {
		req.PagesPerBatch = 10
	}

	slog.Info("banner/ingest",
		"path", req.DocsPath, "overwrite", req.Overwrite,
		"pages_per_batch", req.PagesPerBatch, "start", req.StartPage, "end", req.EndPage,
	)

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

	slog.Info("banner/blob/sync", "container", req.ContainerName, "prefix", req.Prefix)
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
		slog.Error("banner/summarize/full failed", "error", err)
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
		slog.Error("banner/summarize failed", "topic", topic, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// StudentProcedure godoc
//
//	@Summary	Get step-by-step instructions for a Banner Student task
//	@Tags		student
//	@Accept		json
//	@Produce	json
//	@Param		body	body		rag.ProcedureRequest	true	"Procedure request"
//	@Success	200		{object}	rag.ProcedureResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/student/procedure [post]
func (h *Handler) StudentProcedure(c *gin.Context) {
	var req rag.ProcedureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/student/procedure", "topic", req.Topic)

	resp, err := h.pipeline.StudentProcedure(req)
	if err != nil {
		slog.Error("banner/student/procedure failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/student/procedure done", "sources", resp.RetrievalCount)
	c.JSON(http.StatusOK, resp)
}

// StudentLookup godoc
//
//	@Summary	Look up a Banner Student concept or feature definition
//	@Tags		student
//	@Accept		json
//	@Produce	json
//	@Param		body	body		rag.LookupRequest	true	"Lookup request"
//	@Success	200		{object}	rag.LookupResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/student/lookup [post]
func (h *Handler) StudentLookup(c *gin.Context) {
	var req rag.LookupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/student/lookup", "term", req.Term)

	resp, err := h.pipeline.StudentLookup(req)
	if err != nil {
		slog.Error("banner/student/lookup failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/student/lookup done", "sources", resp.RetrievalCount)
	c.JSON(http.StatusOK, resp)
}

// StudentCrossReference godoc
//
//	@Summary	Analyse how a Banner release change affects Student user guide procedures
//	@Tags		student
//	@Accept		json
//	@Produce	json
//	@Param		body	body		rag.CrossReferenceRequest	true	"Cross-reference request"
//	@Success	200		{object}	rag.CrossReferenceResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/student/cross-reference [post]
func (h *Handler) StudentCrossReference(c *gin.Context) {
	var req rag.CrossReferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/student/cross-reference",
		"question", req.Question, "version", req.VersionFilter, "module", req.ModuleFilter,
	)

	resp, err := h.pipeline.StudentCrossReference(req)
	if err != nil {
		slog.Error("banner/student/cross-reference failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/student/cross-reference done",
		"release_sources", len(resp.ReleaseSources), "guide_sources", len(resp.GuideSources),
	)
	c.JSON(http.StatusOK, resp)
}

// ─── SOP ──────────────────────────────────────────────────────────────────────

type sopAskRequest struct {
	Question string `json:"question" binding:"required,min=5"`
	TopK     int    `json:"top_k"`
	Mode     string `json:"mode"` // "local" (default), "web", "hybrid"
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

	reqID, _ := c.Get("request_id")
	slog.Info("sop/ask", "question", req.Question, "top_k", req.TopK, "request_id", reqID)

	resp, err := h.pipeline.Ask(rag.AskRequest{
		Question:         req.Question,
		TopK:             req.TopK,
		SourceTypeFilter: azure.SourceTypeSOP,
		Mode:             req.Mode,
	})
	if err != nil {
		slog.Error("sop/ask failed", "error", err, "request_id", reqID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Info("sop/ask done",
		"answer_len", len(resp.Answer), "sources", resp.RetrievalCount,
		"top_score", resp.TopScore, "request_id", reqID,
	)
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
	slog.Info("sop/ingest", "path", sopPath, "overwrite", req.Overwrite)

	result, err := ingest.Run(h.cfg, sopPath, req.Overwrite, 0, 0, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// ─── Student User Guide ───────────────────────────────────────────────────────

type studentAskRequest struct {
	Question      string `json:"question"       binding:"required,min=5"`
	TopK          int    `json:"top_k"`
	VersionFilter string `json:"version_filter"`
	ModuleFilter  string `json:"module_filter"`
	SectionFilter string `json:"section_filter"`
	Mode          string `json:"mode"` // "local" (default), "web", "hybrid", "auto"
}

// StudentAsk godoc
//
//	@Summary	Ask a question against Banner Student user guide
//	@Tags		student
//	@Accept		json
//	@Produce	json
//	@Param		body	body		studentAskRequest	true	"Question payload"
//	@Success	200		{object}	rag.AskResponse
//	@Failure	400		{object}	map[string]string
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/student/ask [post]
func (h *Handler) StudentAsk(c *gin.Context) {
	var req studentAskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TopK == 0 {
		req.TopK = h.cfg.TopKDefault
	}

	slog.Info("banner/student/ask",
		"question", req.Question, "top_k", req.TopK,
		"version", req.VersionFilter, "module", req.ModuleFilter,
	)

	resp, err := h.pipeline.Ask(rag.AskRequest{
		Question:         req.Question,
		TopK:             req.TopK,
		VersionFilter:    req.VersionFilter,
		ModuleFilter:     req.ModuleFilter,
		SectionFilter:    req.SectionFilter,
		SourceTypeFilter: azure.SourceTypeBannerGuide,
		Mode:             req.Mode,
	})
	if err != nil {
		slog.Error("banner/student/ask failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	slog.Info("banner/student/ask done", "answer_len", len(resp.Answer), "sources", resp.RetrievalCount)
	c.JSON(http.StatusOK, resp)
}

// StudentIngest godoc
//
//	@Summary	Ingest Banner Student user guide PDFs into the search index
//	@Tags		student
//	@Accept		json
//	@Produce	json
//	@Param		body	body		ingestRequest	false	"Ingest options"
//	@Success	200		{object}	map[string]any
//	@Failure	500		{object}	map[string]string
//	@Router		/banner/student/ingest [post]
func (h *Handler) StudentIngest(c *gin.Context) {
	var req ingestRequest
	_ = c.ShouldBindJSON(&req)
	if req.DocsPath == "" {
		req.DocsPath = "data/docs/banner/student/use"
	}
	if req.PagesPerBatch == 0 {
		req.PagesPerBatch = 10
	}

	slog.Info("banner/student/ingest",
		"path", req.DocsPath, "overwrite", req.Overwrite,
		"pages_per_batch", req.PagesPerBatch, "start", req.StartPage, "end", req.EndPage,
	)

	result, err := ingest.Run(h.cfg, req.DocsPath, req.Overwrite, req.PagesPerBatch, req.StartPage, req.EndPage)
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
