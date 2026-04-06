// internal/websearch/bing.go
// Bing Web Search v7 client — alternative web search backend.
// The active backend is TavilyClient (internal/websearch/tavily.go).
// Both satisfy the WebSearcher interface and are interchangeable.
// Only searches official Ellucian-owned properties to avoid noise from
// third-party forums, resellers, or outdated mirrors.
package websearch

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TrustedEllucianDomains is the allow-list of Ellucian-owned sites used to
// scope web search results. Add or remove entries here to adjust coverage.
var TrustedEllucianDomains = []string{
	"docs.ellucian.com",      // official product documentation
	"community.ellucian.com", // Ellucian Community (knowledge base, forums)
	"ellucian.com",           // main site — product pages, announcements, blog
	"resources.ellucian.com", // resource centre (white papers, guides)
}

// WebSearcher is the interface satisfied by TavilyClient and BingClient.
// The RAG pipeline depends on this interface, not the concrete types.
type WebSearcher interface {
	Search(query string, topK int) ([]WebResult, error)
}

// WebResult is a single web search result returned by any WebSearcher.
type WebResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// BingClient calls the Bing Web Search v7 API.
// Use NewTavilyClient for the active web search backend.
type BingClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewBingClient returns a BingClient ready to use.
// Returns nil when apiKey is empty so callers can guard with a nil check.
func NewBingClient(apiKey string) *BingClient {
	if apiKey == "" {
		return nil
	}
	return &BingClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

const bingSearchEndpoint = "https://api.bing.microsoft.com/v7.0/search"

// Search queries Bing restricted to TrustedEllucianDomains.
// The site filter is prepended to every query so only trusted Ellucian
// properties appear in results.
func (b *BingClient) Search(query string, topK int) ([]WebResult, error) {
	if topK <= 0 {
		topK = 5
	}

	fullQuery := buildSiteFilter(TrustedEllucianDomains) + " " + query

	reqURL := fmt.Sprintf(
		"%s?q=%s&count=%d&mkt=en-US&responseFilter=Webpages",
		bingSearchEndpoint,
		url.QueryEscape(fullQuery),
		topK,
	)

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bing build request: %w", err)
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", b.apiKey)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bing search http: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("bing search HTTP %d: %s", resp.StatusCode, string(body))
	}

	var bingResp struct {
		WebPages struct {
			Value []struct {
				Name    string `json:"name"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			} `json:"value"`
		} `json:"webPages"`
	}
	if err := json.Unmarshal(body, &bingResp); err != nil {
		return nil, fmt.Errorf("parse bing response: %w", err)
	}

	results := make([]WebResult, 0, len(bingResp.WebPages.Value))
	for _, v := range bingResp.WebPages.Value {
		results = append(results, WebResult{
			Title:   v.Name,
			URL:     v.URL,
			Snippet: v.Snippet,
		})
	}
	return results, nil
}

// buildSiteFilter constructs a site: restriction for multiple domains.
// e.g. "(site:docs.ellucian.com OR site:community.ellucian.com)"
func buildSiteFilter(domains []string) string {
	parts := make([]string, len(domains))
	for i, d := range domains {
		parts[i] = "site:" + d
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}
