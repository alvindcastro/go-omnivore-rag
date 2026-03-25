// internal/azure/search.go
// Direct REST calls to Azure AI Search.
// Handles index creation, document upload, and hybrid search.
package azure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"go-omnivore-rag/config"
)

// SearchClient wraps Azure AI Search REST calls.
type SearchClient struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewSearchClient returns a new Search client.
func NewSearchClient(cfg *config.Config) *SearchClient {
	return &SearchClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ─── Index Management ─────────────────────────────────────────────────────────

// CreateIndex creates the banner-upgrade-knowledge index in Azure AI Search.
// Safe to call multiple times — uses PUT (create or update).
func (c *SearchClient) CreateIndex() error {
	// Step 1 — Delete existing index if it exists
	deleteURL := fmt.Sprintf(
		"%s/indexes/%s?api-version=2024-03-01-Preview",
		strings.TrimRight(c.cfg.AzureSearchEndpoint, "/"),
		c.cfg.AzureSearchIndexName,
	)

	deleteReq, _ := http.NewRequest(http.MethodDelete, deleteURL, nil)
	deleteReq.Header.Set("api-key", c.cfg.AzureSearchAPIKey)

	deleteResp, err := c.httpClient.Do(deleteReq)
	if err != nil {
		return fmt.Errorf("delete index http: %w", err)
	}
	deleteResp.Body.Close()
	// 404 is fine — index didn't exist yet
	log.Printf("Delete index status: %d", deleteResp.StatusCode)

	// Step 2 — Wait a moment for deletion to propagate
	time.Sleep(2 * time.Second)

	// Step 3 — Create fresh index
	url := fmt.Sprintf(
		"%s/indexes/%s?api-version=2024-03-01-Preview",
		strings.TrimRight(c.cfg.AzureSearchEndpoint, "/"),
		c.cfg.AzureSearchIndexName,
	)

	// Index schema: text fields + 1536-dim vector field
	indexDef := map[string]any{
		"name": c.cfg.AzureSearchIndexName,
		"fields": []map[string]any{
			{"name": "id", "type": "Edm.String", "key": true, "filterable": true},
			{"name": "filename", "type": "Edm.String", "filterable": true, "sortable": true},
			{"name": "page_number", "type": "Edm.Int32", "filterable": true},
			{"name": "banner_module", "type": "Edm.String", "filterable": true, "facetable": true},
			{"name": "banner_version", "type": "Edm.String", "filterable": true, "facetable": true},
			{"name": "year", "type": "Edm.String", "filterable": true, "facetable": true},
			{
				"name":       "chunk_text",
				"type":       "Edm.String",
				"searchable": true,
				"analyzer":   "en.microsoft",
			},
			{
				"name":                "content_vector",
				"type":                "Collection(Edm.Single)",
				"searchable":          true,
				"dimensions":          1536,
				"vectorSearchProfile": "banner-vector-profile",
			},
		},
		"vectorSearch": map[string]any{
			"algorithms": []map[string]any{
				{
					"name": "banner-hnsw",
					"kind": "hnsw",
					"hnswParameters": map[string]any{
						"metric":         "cosine",
						"m":              4,
						"efConstruction": 400,
						"efSearch":       500,
					},
				},
			},
			"profiles": []map[string]any{
				{
					"name":      "banner-vector-profile",
					"algorithm": "banner-hnsw",
				},
			},
		},
	}

	payload, _ := json.Marshal(indexDef)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.cfg.AzureSearchAPIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("create index http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create index HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// GetDocumentCount returns the number of documents indexed.
func (c *SearchClient) GetDocumentCount() (int64, error) {
	url := fmt.Sprintf(
		"%s/indexes/%s/docs/$count?api-version=2024-03-01-Preview",
		strings.TrimRight(c.cfg.AzureSearchEndpoint, "/"),
		c.cfg.AzureSearchIndexName,
	)

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("api-key", c.cfg.AzureSearchAPIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var count int64
	if err := json.Unmarshal(body, &count); err != nil {
		return 0, fmt.Errorf("parse count: %w", err)
	}
	return count, nil
}

// ─── Document Upload ──────────────────────────────────────────────────────────

// ChunkDocument represents a single indexed chunk.
type ChunkDocument struct {
	ID            string    `json:"id"`
	Filename      string    `json:"filename"`
	PageNumber    int       `json:"page_number"`
	BannerModule  string    `json:"banner_module"`
	BannerVersion string    `json:"banner_version"`
	Year          string    `json:"year"`
	ChunkText     string    `json:"chunk_text"`
	ContentVector []float32 `json:"content_vector"`
}

// UploadDocuments uploads a batch of chunks to the search index.
func (c *SearchClient) UploadDocuments(docs []ChunkDocument) error {
	url := fmt.Sprintf(
		"%s/indexes/%s/docs/index?api-version=2024-03-01-Preview",
		strings.TrimRight(c.cfg.AzureSearchEndpoint, "/"),
		c.cfg.AzureSearchIndexName,
	)

	// Wrap each doc with the "upload" action
	type actionDoc struct {
		Action string `json:"@search.action"`
		ChunkDocument
	}
	actions := make([]actionDoc, len(docs))
	for i, d := range docs {
		actions[i] = actionDoc{Action: "mergeOrUpload", ChunkDocument: d}
	}

	body := map[string]any{"value": actions}
	payload, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.cfg.AzureSearchAPIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload docs http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload docs HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ─── Hybrid Search ────────────────────────────────────────────────────────────

// SearchResult is a single result returned from the search index.
type SearchResult struct {
	ID            string  `json:"id"`
	Filename      string  `json:"filename"`
	PageNumber    int     `json:"page_number"`
	BannerModule  string  `json:"banner_module"`
	BannerVersion string  `json:"banner_version"`
	Year          string  `json:"year"`
	ChunkText     string  `json:"chunk_text"`
	Score         float64 `json:"@search.score"`
}

// HybridSearch runs keyword + vector search and returns the top-K results.
func (c *SearchClient) HybridSearch(
	queryText string,
	queryVector []float32,
	topK int,
	versionFilter string,
	moduleFilter string,
	yearFilter string,
) ([]SearchResult, error) {
	url := fmt.Sprintf(
		"%s/indexes/%s/docs/search?api-version=2024-03-01-Preview",
		strings.TrimRight(c.cfg.AzureSearchEndpoint, "/"),
		c.cfg.AzureSearchIndexName,
	)

	searchBody := map[string]any{
		"search": queryText, // BM25 keyword leg
		"top":    topK,
		"select": "id,filename,page_number,banner_module,banner_version,year,chunk_text",
		"vectorQueries": []map[string]any{
			{
				"kind":   "vector",
				"vector": queryVector,
				"fields": "content_vector",
				"k":      topK,
			},
		},
	}

	// Build OData filter
	filters := []string{}
	if versionFilter != "" {
		filters = append(filters, fmt.Sprintf("banner_version eq '%s'", versionFilter))
	}
	if moduleFilter != "" {
		// Capitalize first letter to match stored values e.g. "general" -> "General"
		moduleFilter = strings.ToUpper(moduleFilter[:1]) + strings.ToLower(moduleFilter[1:])
		filters = append(filters, fmt.Sprintf("banner_module eq '%s'", moduleFilter))
	}
	if yearFilter != "" {
		filters = append(filters, fmt.Sprintf("year eq '%s'", yearFilter)) // add this
	}
	if len(filters) > 0 {
		searchBody["filter"] = strings.Join(filters, " and ")
	}

	payload, _ := json.Marshal(searchBody)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.cfg.AzureSearchAPIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("search HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var searchResp struct {
		Value []SearchResult `json:"value"`
	}
	if err := json.Unmarshal(respBytes, &searchResp); err != nil {
		return nil, fmt.Errorf("parse search response: %w", err)
	}

	return searchResp.Value, nil
}
