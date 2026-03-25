// internal/rag/summarize.go
// Summarization pipeline — retrieves indexed chunks for a document
// and generates focused summaries using GPT-4o-mini.
package rag

import (
	"fmt"
	"log"
	"strings"

	"go-omnivore-rag/internal/azure"
)

// ─── Request / Response Types ─────────────────────────────────────────────────

// SummarizeRequest is shared across all summarize endpoints.
type SummarizeRequest struct {
	Filename      string `json:"filename"  binding:"required"`
	BannerModule  string `json:"banner_module"`
	BannerVersion string `json:"banner_version"`
	YearFilter    string `json:"year_filter"`
	TopK          int    `json:"top_k"`
}

// SummarizeResponse is returned by each focused endpoint.
type SummarizeResponse struct {
	Filename       string `json:"filename"`
	BannerModule   string `json:"banner_module"`
	BannerVersion  string `json:"banner_version"`
	Topic          string `json:"topic"`
	Summary        string `json:"summary"`
	SourcePages    []int  `json:"source_pages"`
	ChunksAnalyzed int    `json:"chunks_analyzed"`
}

// FullSummaryResponse is returned by /summarize/full.
type FullSummaryResponse struct {
	Filename        string `json:"filename"`
	BannerModule    string `json:"banner_module"`
	BannerVersion   string `json:"banner_version"`
	WhatChanged     string `json:"what_changed"`
	BreakingChanges string `json:"breaking_changes"`
	ActionItems     string `json:"action_items"`
	Compatibility   string `json:"compatibility"`
	SourcePages     []int  `json:"source_pages"`
	ChunksAnalyzed  int    `json:"chunks_analyzed"`
}

// ─── Topic Configs ────────────────────────────────────────────────────────────

type topicConfig struct {
	searchQuery  string
	systemPrompt string
}

var topics = map[string]topicConfig{
	"changes": {
		searchQuery: "new features enhancements updates what changed improvements",
		systemPrompt: `You are an Ellucian Banner ERP analyst helping IT staff prepare for upgrades.
Based on the provided release note excerpts, summarize ONLY the new features,
enhancements, and changes introduced in this release.
Use clear bullet points starting with -.
Be specific and concise. Focus only on what is NEW or CHANGED.`,
	},
	"breaking": {
		searchQuery: "breaking changes removed deprecated end of support no longer supported",
		systemPrompt: `You are an Ellucian Banner ERP analyst helping IT staff prepare for upgrades.
Based on the provided release note excerpts, identify ONLY breaking changes,
deprecated features, removed functionality, and end-of-support notices.
Use clear bullet points starting with -.
If none are found, respond with: "No breaking changes identified in the provided excerpts."`,
	},
	"actions": {
		searchQuery: "action required steps must do before after upgrade install configure script",
		systemPrompt: `You are an Ellucian Banner ERP analyst helping IT staff prepare for upgrades.
Based on the provided release note excerpts, list ONLY the specific action items
that IT staff must perform before or after upgrading.
Use numbered list format (1. 2. 3.).
Be specific — include script names, page names, or configuration steps where mentioned.
If none are found, respond with: "No specific action items identified in the provided excerpts."`,
	},
	"compatibility": {
		searchQuery: "compatibility prerequisites version requirements database Oracle Java supported",
		systemPrompt: `You are an Ellucian Banner ERP analyst helping IT staff prepare for upgrades.
Based on the provided release note excerpts, summarize ONLY compatibility requirements,
prerequisites, version dependencies, database requirements, and supported configurations.
Use clear bullet points starting with -.
If none are found, respond with: "No compatibility information identified in the provided excerpts."`,
	},
}

// ─── Summarizer ───────────────────────────────────────────────────────────────

// Summarizer holds Azure clients needed for summarization.
type Summarizer struct {
	openai *azure.OpenAIClient
	search *azure.SearchClient
}

// NewSummarizer creates a new Summarizer.
func NewSummarizer(openai *azure.OpenAIClient, search *azure.SearchClient) *Summarizer {
	return &Summarizer{openai: openai, search: search}
}

// SummarizeTopic runs the summarization pipeline for a specific topic.
func (s *Summarizer) SummarizeTopic(req SummarizeRequest, topic string) (*SummarizeResponse, error) {
	cfg, ok := topics[topic]
	if !ok {
		return nil, fmt.Errorf("unknown topic: %s (valid: changes, breaking, actions, compatibility)", topic)
	}

	if req.TopK == 0 {
		req.TopK = 20
	}

	chunks, pages, err := s.retrieveChunks(req, cfg.searchQuery)
	if err != nil {
		return nil, err
	}

	if len(chunks) == 0 {
		return &SummarizeResponse{
			Filename:       req.Filename,
			BannerModule:   req.BannerModule,
			BannerVersion:  req.BannerVersion,
			Topic:          topic,
			Summary:        "No relevant content found. Please ensure the document has been ingested.",
			SourcePages:    []int{},
			ChunksAnalyzed: 0,
		}, nil
	}

	summary, err := s.generate(cfg.systemPrompt, chunks)
	if err != nil {
		return nil, err
	}

	return &SummarizeResponse{
		Filename:       req.Filename,
		BannerModule:   req.BannerModule,
		BannerVersion:  req.BannerVersion,
		Topic:          topic,
		Summary:        summary,
		SourcePages:    pages,
		ChunksAnalyzed: len(chunks),
	}, nil
}

// SummarizeFull runs all four topics and returns a combined response.
func (s *Summarizer) SummarizeFull(req SummarizeRequest) (*FullSummaryResponse, error) {
	if req.TopK == 0 {
		req.TopK = 20
	}

	// Retrieve chunks once with a broad query
	broadQuery := "new features changes breaking deprecated prerequisites compatibility action items upgrade steps"
	chunks, pages, err := s.retrieveChunks(req, broadQuery)
	if err != nil {
		return nil, err
	}

	if len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks found for filename: %s", req.Filename)
	}

	// Build context once — reuse for all four topics
	context := buildContext(chunks)

	log.Printf("  Generating full summary from %d chunks...", len(chunks))

	// Generate all four summaries
	whatChanged, err := s.generateFromContext(topics["changes"].systemPrompt, context)
	if err != nil {
		return nil, fmt.Errorf("changes summary: %w", err)
	}
	log.Printf("  ✓ What changed done")

	breaking, err := s.generateFromContext(topics["breaking"].systemPrompt, context)
	if err != nil {
		return nil, fmt.Errorf("breaking summary: %w", err)
	}
	log.Printf("  ✓ Breaking changes done")

	actions, err := s.generateFromContext(topics["actions"].systemPrompt, context)
	if err != nil {
		return nil, fmt.Errorf("actions summary: %w", err)
	}
	log.Printf("  ✓ Action items done")

	compatibility, err := s.generateFromContext(topics["compatibility"].systemPrompt, context)
	if err != nil {
		return nil, fmt.Errorf("compatibility summary: %w", err)
	}
	log.Printf("  ✓ Compatibility done")

	return &FullSummaryResponse{
		Filename:        req.Filename,
		BannerModule:    req.BannerModule,
		BannerVersion:   req.BannerVersion,
		WhatChanged:     whatChanged,
		BreakingChanges: breaking,
		ActionItems:     actions,
		Compatibility:   compatibility,
		SourcePages:     pages,
		ChunksAnalyzed:  len(chunks),
	}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *Summarizer) retrieveChunks(req SummarizeRequest, query string) ([]azure.SearchResult, []int, error) {
	log.Printf("Retrieving chunks for summarize | topic query: %.30s... | file: %s", query, req.Filename)

	vector, err := s.openai.EmbedText(query)
	if err != nil {
		return nil, nil, fmt.Errorf("embed failed: %w", err)
	}

	results, err := s.search.HybridSearch(
		query,
		vector,
		req.TopK,
		req.BannerVersion,
		req.BannerModule,
		req.YearFilter,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("search failed: %w", err)
	}

	// Filter by filename
	var filtered []azure.SearchResult
	pageSet := map[int]bool{}
	for _, r := range results {
		if strings.EqualFold(r.Filename, req.Filename) {
			filtered = append(filtered, r)
			pageSet[r.PageNumber] = true
		}
	}

	pageList := []int{}
	for p := range pageSet {
		pageList = append(pageList, p)
	}

	log.Printf("  Retrieved %d chunks from %d pages", len(filtered), len(pageList))
	return filtered, pageList, nil
}

func (s *Summarizer) generate(systemPrompt string, chunks []azure.SearchResult) (string, error) {
	return s.generateFromContext(systemPrompt, buildContext(chunks))
}

func (s *Summarizer) generateFromContext(systemPrompt, context string) (string, error) {
	userMessage := fmt.Sprintf("Release note excerpts:\n\n%s\n\nProvide your summary now.", context)

	answer, err := s.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}

func buildContext(chunks []azure.SearchResult) string {
	var sb strings.Builder
	for i, r := range chunks {
		sb.WriteString(fmt.Sprintf("[%d] Page %d:\n%s\n\n", i+1, r.PageNumber, r.ChunkText))
	}
	return sb.String()
}
