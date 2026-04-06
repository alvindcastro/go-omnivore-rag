// internal/websearch/tavily.go
// Tavily web search client scoped to trusted Ellucian domains.
// Implements WebSearcher — the active web search backend.
package websearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const tavilySearchEndpoint = "https://api.tavily.com/search"

// TavilyClient calls the Tavily Search API.
type TavilyClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewTavilyClient returns a TavilyClient ready to use.
// Returns nil when apiKey is empty so callers can guard with a nil check.
func NewTavilyClient(apiKey string) *TavilyClient {
	if apiKey == "" {
		return nil
	}
	return &TavilyClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Search queries Tavily restricted to TrustedEllucianDomains.
// Returns []WebResult to satisfy the WebSearcher interface.
func (t *TavilyClient) Search(query string, topK int) ([]WebResult, error) {
	if topK <= 0 {
		topK = 5
	}

	body, err := json.Marshal(map[string]any{
		"query":           query,
		"include_domains": TrustedEllucianDomains,
		"max_results":     topK,
		"search_depth":    "basic",
	})
	if err != nil {
		return nil, fmt.Errorf("tavily marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, tavilySearchEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tavily build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily search http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tavily search HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tavilyResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &tavilyResp); err != nil {
		return nil, fmt.Errorf("parse tavily response: %w", err)
	}

	results := make([]WebResult, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		results = append(results, WebResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}
	return results, nil
}
