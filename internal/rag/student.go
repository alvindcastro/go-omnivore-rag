// internal/rag/student.go
// RAG pipelines for the Banner Student user guide.
// Three query modes:
//   - Procedure: step-by-step how-to instructions
//   - Lookup:    concept / feature definition
//   - CrossReference: impact analysis across release notes + user guide
package rag

import (
	"fmt"
	"strings"

	"go-omnivore-rag/internal/azure"
)

// ─── Request / Response Types ─────────────────────────────────────────────────

// ProcedureRequest asks for step-by-step instructions for a Banner Student task.
type ProcedureRequest struct {
	Topic         string `json:"topic"          binding:"required,min=5"`
	TopK          int    `json:"top_k"`
	ModuleFilter  string `json:"module_filter"`
	VersionFilter string `json:"version_filter"`
	SectionFilter string `json:"section_filter"`
}

// ProcedureResponse returns numbered steps extracted from the user guide.
type ProcedureResponse struct {
	Topic          string        `json:"topic"`
	Procedure      string        `json:"procedure"`
	Sources        []SourceChunk `json:"sources"`
	RetrievalCount int           `json:"retrieval_count"`
}

// LookupRequest asks for a definition or explanation of a Banner Student concept.
type LookupRequest struct {
	Term          string `json:"term"           binding:"required,min=2"`
	TopK          int    `json:"top_k"`
	ModuleFilter  string `json:"module_filter"`
	VersionFilter string `json:"version_filter"`
	SectionFilter string `json:"section_filter"`
}

// LookupResponse returns a definition grounded in the user guide.
type LookupResponse struct {
	Term           string        `json:"term"`
	Definition     string        `json:"definition"`
	Sources        []SourceChunk `json:"sources"`
	RetrievalCount int           `json:"retrieval_count"`
}

// CrossReferenceRequest asks how a release note change affects a Banner Student procedure.
type CrossReferenceRequest struct {
	Question      string `json:"question"       binding:"required,min=5"`
	TopK          int    `json:"top_k"`
	VersionFilter string `json:"version_filter"`
	ModuleFilter  string `json:"module_filter"`
}

// CrossReferenceResponse returns an analysis with sources from each document type.
type CrossReferenceResponse struct {
	Question       string        `json:"question"`
	Analysis       string        `json:"analysis"`
	ReleaseSources []SourceChunk `json:"release_sources"`
	GuideSources   []SourceChunk `json:"guide_sources"`
	RetrievalCount int           `json:"retrieval_count"`
}

// ─── System Prompts ───────────────────────────────────────────────────────────

const procedureSystemPrompt = `You are a Banner ERP trainer helping IT staff and end users.
The user is asking how to perform a specific task in Banner Student.

Rules:
- Answer ONLY using the provided documentation excerpts.
- Format your response as a numbered list of steps (1. 2. 3. ...).
- Start with any prerequisites or warnings before the steps.
- Be specific: include menu paths, form names, field names, and button labels exactly as they appear.
- If the documentation does not describe the full procedure, say so and provide what is available.`

const lookupSystemPrompt = `You are a Banner ERP documentation assistant.
The user wants to understand what a specific Banner Student concept, term, or feature is.

Rules:
- Answer ONLY using the provided documentation excerpts.
- Start with a concise one-sentence definition.
- Then expand with how it works, where it is used, and any important constraints.
- Use plain language suitable for both technical staff and end users.
- If the term is not covered in the documentation, say so clearly.`

const crossReferenceSystemPrompt = `You are a Banner ERP upgrade analyst.
You have been given two sets of excerpts: Banner release notes and the Banner Student user guide.
Your job is to analyse how the release note changes affect the procedures described in the user guide.

Rules:
- Answer ONLY using the provided excerpts from both sources.
- Identify specific procedures or features in the user guide that are affected by the release note changes.
- If a change introduces new steps, modified behaviour, or removes functionality, state it explicitly.
- If the release notes do not appear to affect the user guide content, say so clearly.
- Cite sources using [R1], [R2]... for release note excerpts and [G1], [G2]... for user guide excerpts.`

// ─── Pipeline Methods ─────────────────────────────────────────────────────────

// StudentProcedure retrieves step-by-step instructions for a Banner Student task.
func (p *Pipeline) StudentProcedure(req ProcedureRequest) (*ProcedureResponse, error) {
	if req.TopK == 0 {
		req.TopK = 7
	}

	askResp, err := p.askWithPrompt(AskRequest{
		Question:         req.Topic,
		TopK:             req.TopK,
		ModuleFilter:     req.ModuleFilter,
		VersionFilter:    req.VersionFilter,
		SectionFilter:    req.SectionFilter,
		SourceTypeFilter: azure.SourceTypeBannerGuide,
	}, procedureSystemPrompt)
	if err != nil {
		return nil, err
	}

	return &ProcedureResponse{
		Topic:          req.Topic,
		Procedure:      askResp.Answer,
		Sources:        askResp.Sources,
		RetrievalCount: askResp.RetrievalCount,
	}, nil
}

// StudentLookup retrieves a definition or explanation of a Banner Student concept.
func (p *Pipeline) StudentLookup(req LookupRequest) (*LookupResponse, error) {
	if req.TopK == 0 {
		req.TopK = 5
	}

	askResp, err := p.askWithPrompt(AskRequest{
		Question:         req.Term,
		TopK:             req.TopK,
		ModuleFilter:     req.ModuleFilter,
		VersionFilter:    req.VersionFilter,
		SectionFilter:    req.SectionFilter,
		SourceTypeFilter: azure.SourceTypeBannerGuide,
	}, lookupSystemPrompt)
	if err != nil {
		return nil, err
	}

	return &LookupResponse{
		Term:           req.Term,
		Definition:     askResp.Answer,
		Sources:        askResp.Sources,
		RetrievalCount: askResp.RetrievalCount,
	}, nil
}

// StudentCrossReference queries both release notes and the user guide, then
// analyses how the release changes affect documented Student procedures.
func (p *Pipeline) StudentCrossReference(req CrossReferenceRequest) (*CrossReferenceResponse, error) {
	if req.TopK == 0 {
		req.TopK = 5
	}

	// Embed once — reuse vector for both searches.
	vector, err := p.openai.EmbedText(req.Question)
	if err != nil {
		return nil, fmt.Errorf("embed question: %w", err)
	}

	// Search release notes and user guide in parallel — both use the same embedding.
	type searchResult struct {
		results []azure.SearchResult
		err     error
	}
	releaseCh := make(chan searchResult, 1)
	guideCh := make(chan searchResult, 1)

	go func() {
		r, e := p.search.HybridSearch(
			req.Question, vector, req.TopK,
			req.VersionFilter, req.ModuleFilter, "", azure.SourceTypeBanner, "",
		)
		releaseCh <- searchResult{r, e}
	}()
	go func() {
		r, e := p.search.HybridSearch(
			req.Question, vector, req.TopK,
			req.VersionFilter, req.ModuleFilter, "", azure.SourceTypeBannerGuide, "",
		)
		guideCh <- searchResult{r, e}
	}()

	releaseRes := <-releaseCh
	if releaseRes.err != nil {
		return nil, fmt.Errorf("release note search: %w", releaseRes.err)
	}
	guideRes := <-guideCh
	if guideRes.err != nil {
		return nil, fmt.Errorf("user guide search: %w", guideRes.err)
	}
	releaseResults := releaseRes.results
	guideResults := guideRes.results

	if len(releaseResults) == 0 && len(guideResults) == 0 {
		return &CrossReferenceResponse{
			Question: req.Question,
			Analysis: "No relevant content found in either release notes or the user guide. " +
				"Please ensure both document types have been ingested.",
			ReleaseSources: []SourceChunk{},
			GuideSources:   []SourceChunk{},
		}, nil
	}

	// Build combined prompt with labelled sections.
	userMessage := buildCrossReferencePrompt(req.Question, releaseResults, guideResults)

	analysis, err := p.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: crossReferenceSystemPrompt},
		{Role: "user", Content: userMessage},
	})
	if err != nil {
		return nil, fmt.Errorf("chat complete: %w", err)
	}

	return &CrossReferenceResponse{
		Question:       req.Question,
		Analysis:       strings.TrimSpace(analysis),
		ReleaseSources: toSourceChunks(releaseResults),
		GuideSources:   toSourceChunks(guideResults),
		RetrievalCount: len(releaseResults) + len(guideResults),
	}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func buildCrossReferencePrompt(question string, releaseResults, guideResults []azure.SearchResult) string {
	var sb strings.Builder

	sb.WriteString("=== RELEASE NOTE EXCERPTS ===\n\n")
	for i, r := range releaseResults {
		sb.WriteString(fmt.Sprintf("[R%d] %s (page %d)", i+1, r.Filename, r.PageNumber))
		if r.BannerVersion != "" {
			sb.WriteString(fmt.Sprintf(" | Version: %s", r.BannerVersion))
		}
		sb.WriteString("\n" + r.ChunkText + "\n\n")
	}
	if len(releaseResults) == 0 {
		sb.WriteString("(no release note excerpts found)\n\n")
	}

	sb.WriteString("=== USER GUIDE EXCERPTS ===\n\n")
	for i, r := range guideResults {
		sb.WriteString(fmt.Sprintf("[G%d] %s (page %d)", i+1, r.Filename, r.PageNumber))
		if r.BannerModule != "" {
			sb.WriteString(fmt.Sprintf(" | Module: %s", r.BannerModule))
		}
		sb.WriteString("\n" + r.ChunkText + "\n\n")
	}
	if len(guideResults) == 0 {
		sb.WriteString("(no user guide excerpts found)\n\n")
	}

	sb.WriteString("=== QUESTION ===\n")
	sb.WriteString(question)
	sb.WriteString("\n\n=== ANALYSIS ===")

	return sb.String()
}

func toSourceChunks(results []azure.SearchResult) []SourceChunk {
	chunks := make([]SourceChunk, len(results))
	for i, r := range results {
		chunks[i] = SourceChunk{
			Filename:      r.Filename,
			Page:          r.PageNumber,
			BannerModule:  r.BannerModule,
			BannerVersion: r.BannerVersion,
			Year:          r.Year,
			SourceType:    r.SourceType,
			SOPNumber:     r.SOPNumber,
			DocumentTitle: r.DocumentTitle,
			Section:       r.Section,
			ChunkText:     r.ChunkText,
			Score:         r.Score,
		}
	}
	return chunks
}
