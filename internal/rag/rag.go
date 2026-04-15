// internal/rag/rag.go
// Core RAG pipeline:
//  1. Embed the user's question
//  2. Hybrid search (vector + keyword) against Azure AI Search
//  3. Build a grounded prompt from retrieved chunks
//  4. Call GPT-4o-mini for the final answer
//
// Three retrieval modes are supported:
//   - ModeLocal   (default): Azure AI Search only
//   - ModeWeb:               Tavily web search scoped to trusted Ellucian domains
//   - ModeHybrid:            Both in parallel, merged into a single grounded answer
package rag

import (
	"fmt"
	"strings"

	"go-omnivore-rag/internal/azure"
	"go-omnivore-rag/internal/websearch"
)

// ─── Mode constants ────────────────────────────────────────────────────────────

const (
	ModeLocal  = "local"
	ModeWeb    = "web"
	ModeHybrid = "hybrid"
	ModeAuto   = "auto" // system selects mode based on local retrieval confidence
)

// Routing reasons reported in RoutingDecision.Reason.
const (
	ReasonHighLocalConfidence    = "high_local_confidence"
	ReasonPartialLocalConfidence = "partial_local_confidence"
	ReasonLowLocalConfidence     = "low_local_confidence"
	ReasonNoLocalResults         = "no_local_results"
	ReasonWebUnavailable         = "web_unavailable_fallback_local"
	ReasonWebError               = "web_error_fallback_local"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// AskRequest is the input to the RAG pipeline.
type AskRequest struct {
	Question         string `json:"question"`
	TopK             int    `json:"top_k"`
	VersionFilter    string `json:"version_filter"`
	ModuleFilter     string `json:"module_filter"`
	YearFilter       string `json:"year_filter"`
	SourceTypeFilter string `json:"source_type"`    // "banner", "banner_user_guide", "sop", or "" for all
	SectionFilter    string `json:"section_filter"` // user guide section heading (optional)
	Mode             string `json:"mode"`           // "local" (default), "web", "hybrid"
}

// SourceChunk is a single retrieved document chunk returned to the caller.
type SourceChunk struct {
	Filename      string  `json:"filename"`
	Page          int     `json:"page"`
	BannerModule  string  `json:"banner_module,omitempty"`
	BannerVersion string  `json:"banner_version,omitempty"`
	Year          string  `json:"year,omitempty"`
	SourceType    string  `json:"source_type"`
	SOPNumber     string  `json:"sop_number,omitempty"`
	DocumentTitle string  `json:"document_title,omitempty"`
	Section       string  `json:"section,omitempty"`
	ChunkText     string  `json:"chunk_text"`
	Score         float64 `json:"score"`
}

// WebChunk is a single result from a web search.
type WebChunk struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// RoutingDecision records how auto mode selected a retrieval strategy.
// Populated only when mode="auto"; omitted for explicit mode requests.
type RoutingDecision struct {
	ModeUsed         string  `json:"mode_used"`
	Reason           string  `json:"reason"`
	LocalTopScore    float64 `json:"local_top_score"`
	LocalResultCount int     `json:"local_result_count"`
}

// AskResponse is the full RAG response.
type AskResponse struct {
	Question       string           `json:"question"`
	Answer         string           `json:"answer"`
	Sources        []SourceChunk    `json:"sources"`               // local RAG sources; empty for web-only mode
	WebSources     []WebChunk       `json:"web_sources,omitempty"` // web sources; omitted for local mode
	RetrievalCount int              `json:"retrieval_count"`
	TopScore       float64          `json:"top_score,omitempty"` // highest retrieval score; used by n8n conditions and LangGraph routing
	Routing        *RoutingDecision `json:"routing,omitempty"`   // non-nil only for mode=auto
}

// ─── System Prompts ───────────────────────────────────────────────────────────

// localSystemPrompt grounds answers strictly in ingested local documents.
const localSystemPrompt = `You are a knowledgeable assistant for a higher-education IT team.
You answer questions using two types of source documents:
- Banner release notes: upgrade prerequisites, known issues, configuration steps, compatibility.
- Standard Operating Procedures (SOPs): step-by-step procedures for managing applications and servers.

Rules:
- Answer ONLY using the provided context chunks. Do NOT make things up.
- When referencing specific steps or requirements, cite the source document name or SOP number.
- Be concise but thorough. Use numbered lists for multi-step procedures.
- For SOP questions, present steps in order and note any warnings or prerequisites first.
- If the context does not contain enough information to answer, say so clearly.`

// webSystemPrompt is used when answering from Ellucian web sources only.
const webSystemPrompt = `You are an Ellucian Banner expert supporting a higher-education IT team.
Answer questions using the provided web search results drawn exclusively from official Ellucian sources
(docs.ellucian.com, community.ellucian.com, ellucian.com, resources.ellucian.com).

Rules:
- Ground your answer in the provided web results. Cite the source URL when referencing specific information.
- Draw on your Ellucian expertise to provide context, but clearly distinguish between cited sources and general knowledge.
- Be concise but thorough. Use numbered lists for multi-step procedures.
- Note when information may be version-specific and advise users to verify on docs.ellucian.com.
- If the web results don't fully answer the question, say so and recommend checking docs.ellucian.com directly.`

// hybridSystemPrompt merges local institutional docs with Ellucian web sources.
const hybridSystemPrompt = `You are an Ellucian Banner expert supporting a higher-education IT team.
You have two sets of sources:
- LOCAL DOCS: your institution's ingested Banner release notes and SOPs.
- WEB SOURCES: live results from official Ellucian sites (docs.ellucian.com, community.ellucian.com, etc.).

Rules:
- Prioritize local documentation for institution-specific procedures, configurations, and SOPs.
- Use web sources for general Ellucian product information, community best practices, and supplementary context.
- Cite local documents by filename and web sources by URL.
- Be concise but thorough. Use numbered lists for multi-step procedures.
- If the two sources conflict, note the discrepancy and favour the more specific local documentation.
- If neither source fully answers the question, say so clearly.`

// ─── Pipeline ────────────────────────────────────────────────────────────────

// Pipeline holds the Azure clients needed to run the RAG pipeline.
type Pipeline struct {
	openai                  *azure.OpenAIClient
	search                  *azure.SearchClient
	web                     websearch.WebSearcher // nil when no web search key is configured
	confidenceHighThreshold float64               // auto mode: score >= HIGH → local only
	confidenceLowThreshold  float64               // auto mode: score <  LOW  → web only; between → hybrid
}

// NewPipeline creates a new RAG pipeline.
// web may be nil; web and hybrid modes will return an error if it is.
// highThreshold and lowThreshold control auto-mode routing decisions.
func NewPipeline(openai *azure.OpenAIClient, search *azure.SearchClient, web websearch.WebSearcher, highThreshold, lowThreshold float64) *Pipeline {
	return &Pipeline{
		openai:                  openai,
		search:                  search,
		web:                     web,
		confidenceHighThreshold: highThreshold,
		confidenceLowThreshold:  lowThreshold,
	}
}

// Ask runs the RAG pipeline for a question.
// The retrieval mode is determined by req.Mode ("local", "web", or "hybrid").
// An empty or unrecognised mode defaults to local.
// The Banner module (req.ModuleFilter) selects the LLM persona and enriches
// web search queries with a module-specific prefix.
func (p *Pipeline) Ask(req AskRequest) (*AskResponse, error) {
	mod := resolveModule(req.ModuleFilter)

	switch req.Mode {
	case ModeAuto:
		return p.askAuto(req, mod)
	case ModeWeb:
		return p.askWeb(req, mod)
	case ModeHybrid:
		return p.askHybrid(req, mod)
	default:
		return p.askWithPrompt(req, mod.SystemPrompt)
	}
}

// ─── Auto routing ─────────────────────────────────────────────────────────────

// askAuto selects a retrieval mode at runtime based on local search confidence.
//
// Routing tiers (using Azure AI Search RRF hybrid scores):
//
//	score >= HIGH  → local only   (strong match, no need to hit the web)
//	LOW <= score < HIGH → hybrid  (partial coverage — supplement with Tavily)
//	score <  LOW   → web only     (weak signal — go straight to Ellucian web)
//	0 results      → web only
//
// If Tavily is unavailable the pipeline always falls back to local.
// Already-fetched local results are reused when routing to hybrid,
// so there is no double embedding or duplicate search call.
func (p *Pipeline) askAuto(req AskRequest, mod ModuleDef) (*AskResponse, error) {
	if req.TopK == 0 {
		req.TopK = 5
	}

	// Step 1: Embed + local search — always runs, results reused if routing to hybrid.
	vector, err := p.openai.EmbedText(req.Question)
	if err != nil {
		return nil, fmt.Errorf("embed question: %w", err)
	}
	localResults, err := p.search.HybridSearch(
		req.Question, vector, req.TopK,
		req.VersionFilter, req.ModuleFilter, req.YearFilter,
		req.SourceTypeFilter, req.SectionFilter,
	)
	if err != nil {
		return nil, fmt.Errorf("local search: %w", err)
	}

	// Step 2: Score
	topScore := 0.0
	if len(localResults) > 0 {
		topScore = localResults[0].Score
	}
	routing := &RoutingDecision{
		LocalTopScore:    topScore,
		LocalResultCount: len(localResults),
	}

	// Step 3: High confidence → answer from local results only.
	if len(localResults) > 0 && topScore >= p.confidenceHighThreshold {
		routing.ModeUsed = ModeLocal
		routing.Reason = ReasonHighLocalConfidence
		resp, err := p.finishLocalAnswer(req, mod.SystemPrompt, localResults)
		if err != nil {
			return nil, err
		}
		resp.Routing = routing
		return resp, nil
	}

	// Step 4: Tavily unavailable → fall back to local regardless of score.
	if p.web == nil {
		routing.ModeUsed = ModeLocal
		routing.Reason = ReasonWebUnavailable
		resp, err := p.finishLocalAnswer(req, mod.SystemPrompt, localResults)
		if err != nil {
			return nil, err
		}
		resp.Routing = routing
		return resp, nil
	}

	// Step 5: Fire Tavily search (needed for both web-only and hybrid paths).
	webResults, webErr := p.web.Search(mod.SearchPrefix+" "+req.Question, req.TopK)
	if webErr != nil {
		// Tavily failed — degrade gracefully to local rather than surface an error.
		routing.ModeUsed = ModeLocal
		routing.Reason = ReasonWebError
		resp, err := p.finishLocalAnswer(req, mod.SystemPrompt, localResults)
		if err != nil {
			return nil, err
		}
		resp.Routing = routing
		return resp, nil
	}

	// Step 6: No / very low local results → web only.
	if len(localResults) == 0 || topScore < p.confidenceLowThreshold {
		if len(localResults) == 0 {
			routing.Reason = ReasonNoLocalResults
		} else {
			routing.Reason = ReasonLowLocalConfidence
		}
		routing.ModeUsed = ModeWeb
		resp, err := p.finishWebAnswer(req, webResults)
		if err != nil {
			return nil, err
		}
		resp.Routing = routing
		return resp, nil
	}

	// Step 7: Partial confidence → hybrid (reuse already-fetched local results).
	routing.ModeUsed = ModeHybrid
	routing.Reason = ReasonPartialLocalConfidence
	resp, err := p.finishHybridAnswer(req, localResults, webResults)
	if err != nil {
		return nil, err
	}
	resp.Routing = routing
	return resp, nil
}

// finishLocalAnswer generates an answer from already-fetched local search results.
func (p *Pipeline) finishLocalAnswer(req AskRequest, sysPrompt string, results []azure.SearchResult) (*AskResponse, error) {
	if len(results) == 0 {
		return &AskResponse{
			Question:       req.Question,
			Answer:         "I could not find any relevant documentation for your question. Please ensure documents have been ingested into the knowledge base.",
			Sources:        []SourceChunk{},
			RetrievalCount: 0,
		}, nil
	}
	answer, err := p.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: buildLocalPrompt(req.Question, results)},
	})
	if err != nil {
		return nil, fmt.Errorf("chat complete: %w", err)
	}
	sources := toSourceChunks(results)
	return &AskResponse{
		Question:       req.Question,
		Answer:         answer,
		Sources:        sources,
		RetrievalCount: len(sources),
		TopScore:       topResultScore(results),
	}, nil
}

// finishWebAnswer generates an answer from already-fetched Tavily results.
func (p *Pipeline) finishWebAnswer(req AskRequest, results []websearch.WebResult) (*AskResponse, error) {
	if len(results) == 0 {
		return &AskResponse{
			Question:       req.Question,
			Answer:         "No relevant results were found on official Ellucian sites. Try checking docs.ellucian.com directly.",
			Sources:        []SourceChunk{},
			WebSources:     []WebChunk{},
			RetrievalCount: 0,
		}, nil
	}
	answer, err := p.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: webSystemPrompt},
		{Role: "user", Content: buildWebPrompt(req.Question, results)},
	})
	if err != nil {
		return nil, fmt.Errorf("chat complete: %w", err)
	}
	webChunks := toWebChunks(results)
	return &AskResponse{
		Question:       req.Question,
		Answer:         answer,
		Sources:        []SourceChunk{},
		WebSources:     webChunks,
		RetrievalCount: len(webChunks),
	}, nil
}

// finishHybridAnswer generates an answer from already-fetched local + web results.
func (p *Pipeline) finishHybridAnswer(req AskRequest, local []azure.SearchResult, web []websearch.WebResult) (*AskResponse, error) {
	answer, err := p.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: hybridSystemPrompt},
		{Role: "user", Content: buildHybridPrompt(req.Question, local, web)},
	})
	if err != nil {
		return nil, fmt.Errorf("chat complete: %w", err)
	}
	sources := toSourceChunks(local)
	webChunks := toWebChunks(web)
	return &AskResponse{
		Question:       req.Question,
		Answer:         answer,
		Sources:        sources,
		WebSources:     webChunks,
		RetrievalCount: len(sources) + len(webChunks),
		TopScore:       topResultScore(local),
	}, nil
}

// ─── Local retrieval ──────────────────────────────────────────────────────────

// askWithPrompt is the internal implementation of local-mode Ask.
func (p *Pipeline) askWithPrompt(req AskRequest, sysPrompt string) (*AskResponse, error) {
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
		req.SourceTypeFilter,
		req.SectionFilter,
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
	sources := toSourceChunks(results)

	// Step 4: Build grounded prompt
	userMessage := buildLocalPrompt(req.Question, results)

	// Step 5: Generate answer
	answer, err := p.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: sysPrompt},
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
		TopScore:       topResultScore(results),
	}, nil
}

// ─── Web retrieval ────────────────────────────────────────────────────────────

// askWeb answers using only web search results from trusted Ellucian domains.
// mod.SearchPrefix is prepended to the query to scope results to the right Banner module.
func (p *Pipeline) askWeb(req AskRequest, mod ModuleDef) (*AskResponse, error) {
	if p.web == nil {
		return nil, fmt.Errorf("web search is not configured: set TAVILY_API_KEY")
	}
	if req.TopK == 0 {
		req.TopK = 5
	}

	webResults, err := p.web.Search(mod.SearchPrefix+" "+req.Question, req.TopK)
	if err != nil {
		return nil, fmt.Errorf("web search: %w", err)
	}

	if len(webResults) == 0 {
		return &AskResponse{
			Question:   req.Question,
			Answer:     "No relevant results were found on official Ellucian sites for your question. Try checking docs.ellucian.com directly.",
			Sources:    []SourceChunk{},
			WebSources: []WebChunk{},
		}, nil
	}

	userMessage := buildWebPrompt(req.Question, webResults)
	answer, err := p.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: webSystemPrompt},
		{Role: "user", Content: userMessage},
	})
	if err != nil {
		return nil, fmt.Errorf("chat complete: %w", err)
	}

	return &AskResponse{
		Question:       req.Question,
		Answer:         answer,
		Sources:        []SourceChunk{},
		WebSources:     toWebChunks(webResults),
		RetrievalCount: len(webResults),
	}, nil
}

// ─── Hybrid retrieval ─────────────────────────────────────────────────────────

// askHybrid runs local search and web search concurrently, then merges
// both result sets into a single grounded answer.
// mod.SearchPrefix scopes the web query to the relevant Banner module.
func (p *Pipeline) askHybrid(req AskRequest, mod ModuleDef) (*AskResponse, error) {
	if p.web == nil {
		return nil, fmt.Errorf("web search is not configured: set TAVILY_API_KEY")
	}
	if req.TopK == 0 {
		req.TopK = 5
	}

	type localResult struct {
		results []azure.SearchResult
		err     error
	}
	type webResult struct {
		results []websearch.WebResult
		err     error
	}

	localCh := make(chan localResult, 1)
	webCh := make(chan webResult, 1)

	// Local search leg: embed → hybrid search
	go func() {
		vector, err := p.openai.EmbedText(req.Question)
		if err != nil {
			localCh <- localResult{err: fmt.Errorf("embed: %w", err)}
			return
		}
		r, e := p.search.HybridSearch(
			req.Question, vector, req.TopK,
			req.VersionFilter, req.ModuleFilter, req.YearFilter,
			req.SourceTypeFilter, req.SectionFilter,
		)
		localCh <- localResult{results: r, err: e}
	}()

	// Web search leg: Tavily scoped to trusted Ellucian domains, enriched with module prefix
	go func() {
		r, e := p.web.Search(mod.SearchPrefix+" "+req.Question, req.TopK)
		webCh <- webResult{results: r, err: e}
	}()

	local := <-localCh
	web := <-webCh

	if local.err != nil {
		return nil, fmt.Errorf("local search: %w", local.err)
	}
	if web.err != nil {
		return nil, fmt.Errorf("web search: %w", web.err)
	}

	sources := toSourceChunks(local.results)
	webChunks := toWebChunks(web.results)

	userMessage := buildHybridPrompt(req.Question, local.results, web.results)
	answer, err := p.openai.ChatComplete([]azure.ChatMessage{
		{Role: "system", Content: hybridSystemPrompt},
		{Role: "user", Content: userMessage},
	})
	if err != nil {
		return nil, fmt.Errorf("chat complete: %w", err)
	}

	return &AskResponse{
		Question:       req.Question,
		Answer:         answer,
		Sources:        sources,
		WebSources:     webChunks,
		RetrievalCount: len(sources) + len(webChunks),
		TopScore:       topResultScore(local.results),
	}, nil
}

// topResultScore returns the highest search score from a result set, or 0 if empty.
// Used to populate AskResponse.TopScore for n8n conditional nodes and LangGraph routing.
func topResultScore(results []azure.SearchResult) float64 {
	if len(results) == 0 {
		return 0
	}
	return results[0].Score
}

// ─── Prompt builders ──────────────────────────────────────────────────────────

// buildLocalPrompt assembles the context block + question for local-only retrieval.
func buildLocalPrompt(question string, results []azure.SearchResult) string {
	var sb strings.Builder

	sb.WriteString("Use the following Banner documentation excerpts to answer the question.\n\n")
	sb.WriteString("=== CONTEXT ===\n\n")

	for i, r := range results {
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

// buildWebPrompt assembles a prompt from Tavily web search results.
func buildWebPrompt(question string, results []websearch.WebResult) string {
	var sb strings.Builder

	sb.WriteString("Use the following web search results from official Ellucian sources to answer the question.\n\n")
	sb.WriteString("=== WEB SOURCES ===\n\n")

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("[W%d] %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("URL: %s\n", r.URL))
		sb.WriteString(r.Snippet + "\n")
		if i < len(results)-1 {
			sb.WriteString("\n---\n\n")
		}
	}

	sb.WriteString("\n\n=== QUESTION ===\n")
	sb.WriteString(question)
	sb.WriteString("\n\n=== ANSWER ===")

	return sb.String()
}

// buildHybridPrompt assembles a prompt with both local docs and web sources.
func buildHybridPrompt(question string, local []azure.SearchResult, web []websearch.WebResult) string {
	var sb strings.Builder

	sb.WriteString("=== LOCAL DOCS ===\n\n")
	for i, r := range local {
		label := fmt.Sprintf("[L%d] %s", i+1, r.Filename)
		if r.PageNumber > 0 {
			label += fmt.Sprintf(" (page %d)", r.PageNumber)
		}
		if r.BannerVersion != "" {
			label += fmt.Sprintf(" | Version: %s", r.BannerVersion)
		}
		if r.BannerModule != "" {
			label += fmt.Sprintf(" | Module: %s", r.BannerModule)
		}
		sb.WriteString(label + "\n")
		sb.WriteString(r.ChunkText + "\n")
		if i < len(local)-1 {
			sb.WriteString("\n---\n\n")
		}
	}
	if len(local) == 0 {
		sb.WriteString("(no local documentation found)\n")
	}

	sb.WriteString("\n\n=== WEB SOURCES ===\n\n")
	for i, r := range web {
		sb.WriteString(fmt.Sprintf("[W%d] %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("URL: %s\n", r.URL))
		sb.WriteString(r.Snippet + "\n")
		if i < len(web)-1 {
			sb.WriteString("\n---\n\n")
		}
	}
	if len(web) == 0 {
		sb.WriteString("(no web results found)\n")
	}

	sb.WriteString("\n\n=== QUESTION ===\n")
	sb.WriteString(question)
	sb.WriteString("\n\n=== ANSWER ===")

	return sb.String()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func toWebChunks(results []websearch.WebResult) []WebChunk {
	chunks := make([]WebChunk, len(results))
	for i, r := range results {
		chunks[i] = WebChunk{Title: r.Title, URL: r.URL, Snippet: r.Snippet}
	}
	return chunks
}
