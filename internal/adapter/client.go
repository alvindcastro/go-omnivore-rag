package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ragAskResponse mirrors the go-omnivore-rag rag.AskResponse JSON shape exactly.
type ragAskResponse struct {
	Answer         string           `json:"answer"`
	Question       string           `json:"question"`
	RetrievalCount int              `json:"retrieval_count"`
	Sources        []ragSourceChunk `json:"sources"`
}

type ragSourceChunk struct {
	Score         float64 `json:"score"`
	DocumentTitle string  `json:"document_title"`
	Page          int     `json:"page"`
	SopNumber     string  `json:"sop_number"`
	SourceType    string  `json:"source_type"`
}

// bannerAskRequest is the JSON body sent to /banner/ask.
type bannerAskRequest struct {
	Question      string `json:"question"`
	ModuleFilter  string `json:"module_filter,omitempty"`
	VersionFilter string `json:"version_filter,omitempty"`
	YearFilter    string `json:"year_filter,omitempty"`
	TopK          int    `json:"top_k,omitempty"`
}

// AdapterResponse is the contract returned to Botpress callers.
type AdapterResponse struct {
	Answer     string          `json:"answer"`
	Confidence float64         `json:"confidence"`
	Sources    []AdapterSource `json:"sources"`
	Escalate   bool            `json:"escalate"`
}

// AdapterSource is a flattened source citation for Botpress.
type AdapterSource struct {
	Title      string `json:"title"`
	Page       int    `json:"page"`
	SopNumber  string `json:"sop_number,omitempty"`
	SourceType string `json:"source_type"`
}

// AskOptions carries optional filter parameters for banner queries.
type AskOptions struct {
	ModuleFilter  string
	VersionFilter string
	YearFilter    string
	TopK          int
}

// AdapterClient wraps the go-omnivore-rag HTTP API.
// Construct with NewAdapterClient; all methods accept a context for cancellation.
type AdapterClient struct {
	baseURL string
	http    *http.Client
}

// NewAdapterClient returns a client pointed at baseURL (e.g. "http://localhost:8000").
func NewAdapterClient(baseURL string) *AdapterClient {
	return &AdapterClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// AskBanner queries /banner/ask and maps the result to AdapterResponse.
// Confidence = sources[0].score (0.0 if no sources).
// Escalate = true when RetrievalCount == 0 (hard gate) OR Confidence < 0.01 (calibrated floor).
func (c *AdapterClient) AskBanner(ctx context.Context, question string, opts AskOptions) (AdapterResponse, error) {
	body := bannerAskRequest{
		Question:      question,
		ModuleFilter:  opts.ModuleFilter,
		VersionFilter: opts.VersionFilter,
		YearFilter:    opts.YearFilter,
		TopK:          opts.TopK,
	}
	var raw ragAskResponse
	if err := c.post(ctx, "/banner/ask", body, &raw); err != nil {
		return AdapterResponse{}, err
	}
	return mapResponse(raw), nil
}

// AskSop queries /sop/ask and maps the result to AdapterResponse.
func (c *AdapterClient) AskSop(ctx context.Context, question string) (AdapterResponse, error) {
	body := struct {
		Question string `json:"question"`
	}{Question: question}
	var raw ragAskResponse
	if err := c.post(ctx, "/sop/ask", body, &raw); err != nil {
		return AdapterResponse{}, err
	}
	return mapResponse(raw), nil
}

// post encodes body as JSON, POSTs to baseURL+path, and decodes the response into out.
func (c *AdapterClient) post(ctx context.Context, path string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// mapResponse converts a raw ragAskResponse to the public AdapterResponse contract.
func mapResponse(raw ragAskResponse) AdapterResponse {
	var confidence float64
	if len(raw.Sources) > 0 {
		confidence = raw.Sources[0].Score
	}

	// Escalate on hard gate (no docs) or near-zero score (tangential hit).
	// Azure AI Search hybrid scores for this index cluster 0.01–0.05 for valid results.
	// Threshold derived from empirical calibration — see wiki/RUNBOOK.md § Score Distribution.
	escalate := raw.RetrievalCount == 0 || confidence < 0.01

	sources := make([]AdapterSource, len(raw.Sources))
	for i, s := range raw.Sources {
		sources[i] = AdapterSource{
			Title:      s.DocumentTitle,
			Page:       s.Page,
			SopNumber:  s.SopNumber,
			SourceType: s.SourceType,
		}
	}

	return AdapterResponse{
		Answer:     raw.Answer,
		Confidence: confidence,
		Sources:    sources,
		Escalate:   escalate,
	}
}
