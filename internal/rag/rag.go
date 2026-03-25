// internal/rag/rag.go
// Core RAG pipeline:
//  1. Embed the user's question
//  2. Hybrid search (vector + keyword) against Azure AI Search
//  3. Build a grounded prompt from retrieved chunks
//  4. Call GPT-4o-mini for the final answer
package rag

import (
	"fmt"
	"strings"

	"go-omnivore-rag/internal/azure"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// AskRequest is the input to the RAG pipeline.
type AskRequest struct {
	Question      string `json:"question"`
	TopK          int    `json:"top_k"`
	VersionFilter string `json:"version_filter"`
	ModuleFilter  string `json:"module_filter"`
	YearFilter    string `json:"year_filter"`
}

// SourceChunk is a single retrieved document chunk returned to the caller.
type SourceChunk struct {
	Filename      string  `json:"filename"`
	Page          int     `json:"page"`
	BannerModule  string  `json:"banner_module"`
	BannerVersion string  `json:"banner_version"`
	Year          string  `json:"year"`
	ChunkText     string  `json:"chunk_text"`
	Score         float64 `json:"score"`
}

// AskResponse is the full RAG response.
type AskResponse struct {
	Question       string        `json:"question"`
	Answer         string        `json:"answer"`
	Sources        []SourceChunk `json:"sources"`
	RetrievalCount int           `json:"retrieval_count"`
}

// ─── System Prompt ────────────────────────────────────────────────────────────

const systemPrompt = `You are an expert Ellucian Banner ERP upgrade assistant for a higher-education institution.
Your job is to help IT staff and functional analysts answer questions about Banner module upgrades,
patch releases, prerequisites, known issues, configuration steps, and compatibility.

Rules:
- Answer ONLY using the provided context chunks from Banner release notes and documentation.
- If the context does not contain enough information to answer, say so clearly — do NOT make things up.
- When referencing specific steps or requirements, cite the source document name.
- Be concise but thorough. Use numbered lists for multi-step procedures.
- If a version or module is mentioned in the question, focus your answer on that version/module.`

// ─── Pipeline ────────────────────────────────────────────────────────────────

// Pipeline holds the Azure clients needed to run the RAG pipeline.
type Pipeline struct {
	openai *azure.OpenAIClient
	search *azure.SearchClient
}

// NewPipeline creates a new RAG pipeline.
func NewPipeline(openai *azure.OpenAIClient, search *azure.SearchClient) *Pipeline {
	return &Pipeline{openai: openai, search: search}
}

// Ask runs the full RAG pipeline for a question and returns a grounded answer.
func (p *Pipeline) Ask(req AskRequest) (*AskResponse, error) {
	if req.TopK == 0 {
		req.TopK = 5
	}

	// Step 1: Embed the question
	vector, err := p.openai.EmbedText(req.Question)
	if err != nil {
		return nil, fmt.Errorf("embed question: %w", err)
	}

	// Step 2: Hybrid search
	results, err := p.search.HybridSearch(
		req.Question,
		vector,
		req.TopK,
		req.VersionFilter,
		req.ModuleFilter,
		req.YearFilter,
	)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// No results found
	if len(results) == 0 {
		return &AskResponse{
			Question: req.Question,
			Answer: "I could not find any relevant documentation for your question. " +
				"Please ensure Banner upgrade documents have been ingested into the knowledge base.",
			Sources:        []SourceChunk{},
			RetrievalCount: 0,
		}, nil
	}

	// Step 3: Build sources list
	sources := make([]SourceChunk, len(results))
	for i, r := range results {
		sources[i] = SourceChunk{
			Filename:      r.Filename,
			Page:          r.PageNumber,
			BannerModule:  r.BannerModule,
			BannerVersion: r.BannerVersion,
			Year:          r.Year,
			ChunkText:     r.ChunkText,
			Score:         r.Score,
		}
	}

	// Step 4: Build grounded prompt
	userMessage := buildPrompt(req.Question, results)

	// Step 5: Generate answer
	answer, err := p.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMessage},
	})
	if err != nil {
		return nil, fmt.Errorf("chat complete: %w", err)
	}

	return &AskResponse{
		Question:       req.Question,
		Answer:         answer,
		Sources:        sources,
		RetrievalCount: len(sources),
	}, nil
}

// buildPrompt assembles the context block + question into the user message.
func buildPrompt(question string, results []azure.SearchResult) string {
	var sb strings.Builder

	sb.WriteString("Use the following Banner documentation excerpts to answer the question.\n\n")
	sb.WriteString("=== CONTEXT ===\n\n")

	for i, r := range results {
		// Source label
		label := fmt.Sprintf("[%d] %s", i+1, r.Filename)
		if r.PageNumber > 0 {
			label += fmt.Sprintf(" (page %d)", r.PageNumber)
		}
		if r.BannerModule != "" {
			label += fmt.Sprintf(" | Module: %s", r.BannerModule)
		}
		if r.BannerVersion != "" {
			label += fmt.Sprintf(" | Version: %s", r.BannerVersion)
		}

		sb.WriteString(label + "\n")
		sb.WriteString(r.ChunkText + "\n")
		if i < len(results)-1 {
			sb.WriteString("\n---\n\n")
		}
	}

	sb.WriteString("\n\n=== QUESTION ===\n")
	sb.WriteString(question)
	sb.WriteString("\n\n=== ANSWER ===")

	return sb.String()
}
