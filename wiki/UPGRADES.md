# Tech Upgrades & RAG Quality Improvements

Concrete improvements to the Go codebase, RAG pipeline quality, and developer experience.
Ordered roughly by impact and implementation difficulty.

> **Related brainstorm docs:**
> - [OBSERVABILITY.md](OBSERVABILITY.md) — deep dive on reliability, health checks, alerting, resilience, and tracing
> - [DATABASE.md](DATABASE.md) — AI-ready SQL, PostgreSQL + pgvector migration, schema design, and operational data layer

---

## Table of Contents

1. [API Hardening](#api-hardening)
2. [Observability](#observability)
3. [RAG Quality Improvements](#rag-quality-improvements)
4. [Streaming Responses (SSE)](#streaming-responses-sse)
5. [Async Ingestion with Status Polling](#async-ingestion-with-status-polling)
6. [Semantic Caching](#semantic-caching)
7. [Document Lifecycle Management](#document-lifecycle-management)
8. [Developer Experience](#developer-experience)
9. [Priority Matrix](#priority-matrix)

---

## API Hardening

### 1. API Key Authentication Middleware

Every external caller (n8n, LangGraph, Azure Functions, browser) needs auth before endpoints like
`/index/create` and `/banner/ingest` are exposed to the network.

```go
// internal/api/middleware.go
func APIKeyMiddleware(expected string) gin.HandlerFunc {
    return func(c *gin.Context) {
        key := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
        if key != expected {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
            return
        }
        c.Next()
    }
}
```

```go
// internal/api/router.go
r.Use(APIKeyMiddleware(cfg.APIKey))
```

Add `API_KEY` to `config.go` and `.env.example`. Public endpoints (`/health`) can be excluded.

**Role-based keys (stretch goal):** Two keys — a read-only key for Q&A endpoints and a write key
for ingest and index management. Allows sharing Q&A access without exposing destructive ops.

---

### 2. CORS Middleware

Required before any browser-based client (n8n cloud, a web UI, Swagger UI from a different origin)
can call the API.

```go
// go get github.com/gin-contrib/cors
import "github.com/gin-contrib/cors"

r.Use(cors.New(cors.Config{
    AllowOrigins:     cfg.CORSOrigins, // e.g. ["https://n8n.mycompany.com"]
    AllowMethods:     []string{"GET", "POST", "OPTIONS"},
    AllowHeaders:     []string{"Authorization", "Content-Type"},
    AllowCredentials: false,
    MaxAge:           12 * time.Hour,
}))
```

For development, `AllowAllOrigins: true` is fine. Lock it down before exposing to the internet.

---

### 3. Rate Limiting

Prevent runaway clients from burning Azure OpenAI quota. Simple token bucket per API key or IP.

```go
// go get golang.org/x/time/rate
// One limiter per API key stored in a sync.Map
type rateLimiter struct {
    limiters sync.Map
}

func (rl *rateLimiter) get(key string) *rate.Limiter {
    l, _ := rl.limiters.LoadOrStore(key, rate.NewLimiter(rate.Every(time.Second), 10))
    return l.(*rate.Limiter)
}
```

10 requests/second per key is a reasonable starting point — GPT-4o-mini responses take 1–4 seconds
anyway, so burst throughput beyond 10 RPS is unlikely to be legitimate.

---

### 4. Request ID Middleware

Stamp every request with a correlation ID. Logged in go-omnivore-rag and returned as
`X-Request-ID` so n8n workflows and LangGraph agents can correlate calls end-to-end.

```go
func RequestIDMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        id := c.GetHeader("X-Request-ID")
        if id == "" {
            id = uuid.NewString() // go get github.com/google/uuid
        }
        c.Set("request_id", id)
        c.Header("X-Request-ID", id)
        c.Next()
    }
}
```

---

### 5. Pagination

`GET /sop` and `GET /debug/chunks` return unbounded lists. With hundreds of SOPs indexed, this
becomes slow and expensive.

```
GET /sop?offset=0&limit=20
GET /debug/chunks?offset=0&limit=50
```

Response envelope:
```json
{
  "items": [...],
  "total": 142,
  "offset": 0,
  "limit": 20
}
```

Azure AI Search natively supports `$skip` and `$top` — no extra work on the index side.

---

## Observability

### 1. Structured Logging with `slog`

Replace `log.Printf` with Go's stdlib structured logger. Zero new dependencies.

```go
// cmd/main.go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))
slog.SetDefault(logger)
```

Every log line becomes JSON parseable by Azure Monitor, Datadog, or Grafana Loki:
```json
{"time":"2026-04-06T12:00:00Z","level":"INFO","msg":"banner ask","request_id":"abc-123","question_len":45,"mode":"auto","duration_ms":1823}
```

Key fields to log on every RAG call:
- `request_id` — from the middleware
- `mode` — `local`, `web`, `hybrid`, `auto`
- `route_decision` — which mode auto-routing picked and why
- `chunks_retrieved` — number of search results used
- `duration_ms` — end-to-end latency
- `prompt_tokens` / `completion_tokens` — from the OpenAI response

---

### 2. Token Usage Tracking (Cost Per Query)

Azure OpenAI returns token counts in every response. Log them to track cost per endpoint.

```go
// internal/azure/openai.go — already returns usage, just needs logging
type chatResponse struct {
    Usage struct {
        PromptTokens     int `json:"prompt_tokens"`
        CompletionTokens int `json:"completion_tokens"`
        TotalTokens      int `json:"total_tokens"`
    } `json:"usage"`
    // ...
}
```

At text-embedding-ada-002 pricing (~$0.00002/1K tokens) and gpt-4o-mini (~$0.00015/1K input,
$0.00060/1K output), a typical ask costs ~$0.001–0.003. Logging this per request lets you
build a cost dashboard in Azure Monitor.

---

### 3. OpenTelemetry Tracing

Distributed traces show exactly where time is spent: embedding call, search call, chat call.
Invaluable when latency spikes and you need to know if it's OpenAI, Search, or your code.

```go
// go get go.opentelemetry.io/otel
// go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp

tracer := otel.Tracer("go-omnivore-rag")

// In the RAG pipeline
ctx, span := tracer.Start(ctx, "azure.embed")
embedding, err := p.openai.Embed(ctx, question)
span.End()

ctx, span = tracer.Start(ctx, "azure.search")
results, err := p.search.HybridSearch(ctx, embedding, req)
span.End()
```

Export to Azure Monitor (Application Insights) via the OTLP exporter. The same telemetry works
with Jaeger, Grafana Tempo, or any OTLP-compatible backend.

---

### 4. Index Health Dashboard

A periodic check that surfaces problems before users notice:
- Index document count dropped unexpectedly
- Last ingest timestamp is older than N days
- Search latency p99 spiked

Simplest implementation: an ACA Job that calls `/index/stats` every 30 minutes and sends an
alert to Teams/Slack if count < threshold. More sophisticated: push metrics to Azure Monitor
custom metrics and set alert rules in the portal.

---

## RAG Quality Improvements

These are the highest-leverage improvements for answer quality. Each is independent.

### 1. Confidence Score in Response

The search API returns a score for each chunk. Expose the top score (or average of top-k scores)
in the API response so callers can make routing decisions.

Current `AskResponse`:
```json
{"answer": "...", "sources": [...]}
```

Enhanced:
```json
{"answer": "...", "sources": [...], "confidence": 0.042, "mode_used": "local"}
```

This is already used internally for auto-routing (`confidenceHighThreshold`, `confidenceLowThreshold`).
It just needs to be returned to the caller. n8n workflows and LangGraph agents can then branch on it:
"if confidence < 0.01, escalate to human".

**Where to add:** `AskResponse` struct in `internal/rag/rag.go`, propagate from search results.

---

### 2. Query Rewriting

Before embedding the user's question, rewrite it for better retrieval. Conversational questions
like "what about the finance module?" lose context without the prior turn. Even single-turn
questions can be improved: "does Banner 9.3 break anything?" → "Banner General 9.3.37.2 breaking
changes and deprecated features".

```go
// internal/rag/rewrite.go
func (p *Pipeline) rewriteQuery(question string) (string, error) {
    prompt := fmt.Sprintf(`Rewrite the following question to be more specific and retrieve-friendly
for a RAG system about Ellucian Banner ERP release notes and SOPs.
Return only the rewritten question, no explanation.

Original: %s`, question)

    rewritten, err := p.openai.Chat(systemMsg, prompt, 0.0, 100)
    // ...
    return rewritten, err
}
```

**Tradeoff:** Adds one extra LLM call (~50ms, ~$0.0001). Skip for low-confidence auto-mode where
a web search will likely be triggered anyway.

---

### 3. HyDE — Hypothetical Document Embeddings

Instead of embedding the question, generate a *hypothetical answer* and embed that. The theory:
a generated answer is closer in embedding space to actual document chunks than a short question is.

```go
// internal/rag/hyde.go
func (p *Pipeline) hypotheticalAnswer(question string) (string, error) {
    prompt := fmt.Sprintf(`Write a short, factual paragraph that would answer this question about
Ellucian Banner ERP. Use technical language appropriate for IT administrators.
Do not say "I don't know". Generate a plausible answer even if uncertain.

Question: %s`, question)

    return p.openai.Chat(hydeSystemMsg, prompt, 0.0, 200)
}

// Then embed the hypothetical answer, not the question
embedding, err := p.openai.Embed(ctx, hypotheticalAnswer)
```

**When it helps most:** Vague or short questions ("what changed in finance?") where the question
embedding is too generic. **When it hurts:** Very specific queries where the question is already a
good retrieval signal.

**Recommended:** A/B test with and without HyDE on a set of real questions before committing.

---

### 4. Multi-Query Retrieval

Generate multiple variants of the question, retrieve for each, then de-duplicate and merge results.
Catches relevant chunks that only one query formulation would find.

```go
// internal/rag/multiquery.go
func (p *Pipeline) expandQueries(question string) ([]string, error) {
    prompt := fmt.Sprintf(`Generate 3 different ways to phrase this question about Banner ERP.
Return one per line, no numbering.

Question: %s`, question)

    // Parse 3 variants from the response
    // ...
}

func (p *Pipeline) multiQuerySearch(ctx context.Context, question string, req AskRequest) ([]azure.SearchResult, error) {
    queries, _ := p.expandQueries(question)
    queries = append(queries, question) // always include original

    seen := map[string]bool{}
    var merged []azure.SearchResult
    for _, q := range queries {
        emb, _ := p.openai.Embed(ctx, q)
        results, _ := p.search.HybridSearch(ctx, emb, req)
        for _, r := range results {
            if !seen[r.ID] {
                seen[r.ID] = true
                merged = append(merged, r)
            }
        }
    }
    return merged, nil
}
```

**Cost:** 3–4x more embedding calls and search calls. Worth it for complex questions; overkill for
simple lookups. Expose as a `mode: "multi-query"` option so callers can opt in.

---

### 5. Reranking

After hybrid search returns top-k chunks, run a reranker to re-order them by relevance to the
specific question before building the prompt. The search score is a good but imperfect signal.

**Option A: Azure AI Search semantic ranking** (simplest)

Azure AI Search has built-in semantic ranking that re-scores results using a language model.
Enable it in the search request:

```go
// internal/azure/search.go
body["queryType"] = "semantic"
body["semanticConfiguration"] = "my-semantic-config"
body["captions"] = "extractive"
body["answers"] = "extractive"
```

Requires the **Standard tier** of Azure AI Search (not Free). Adds ~100–200ms per search call.

**Option B: External reranker via Cohere or Azure AI**

Send the question + top-20 chunks to a reranker, get back a sorted list, use only the top-5 for
the prompt. More accurate than semantic ranking but adds an extra API call.

```go
// POST https://api.cohere.com/v1/rerank
body := map[string]any{
    "model": "rerank-english-v3.0",
    "query": question,
    "documents": chunks, // top-20 from initial search
    "top_n": 5,
}
```

**When it matters most:** When the initial retrieval is recall-heavy (top-k=20) and you need
precision at the top for the prompt (top-5). Currently the system uses top-k=5 by default, so
reranking over 20 → 5 would noticeably improve answer quality.

---

### 6. Answer Grounding Check (Hallucination Detection)

After generating the answer, verify it is supported by the retrieved chunks. Catches cases where
the model generates plausible-sounding but uncited claims.

```go
// internal/rag/groundcheck.go
func (p *Pipeline) checkGrounding(answer string, chunks []azure.SearchResult) (bool, float64, error) {
    context := buildContext(chunks)
    prompt := fmt.Sprintf(`Given only the following context, is this answer fully supported?
Answer "yes", "partial", or "no". Then give a confidence 0.0–1.0.

Context:
%s

Answer to check:
%s`, context, answer)

    // Parse "yes 0.95" or "partial 0.6" from response
}
```

**Practical use:** Return `grounded: false` in the response when the check fails. Let the caller
decide whether to show a disclaimer or re-query in web mode.

---

### 7. Chunking Quality Improvements

The current Banner PDF chunker is character-based with a fixed 500-character window. Several
improvements would reduce split-mid-sentence artifacts:

**Sentence-boundary chunking:** Split on `.`, `?`, `!` rather than raw character count.
Use a simple sentence tokenizer or regex — no ML needed.

**Structural chunking for PDFs:** Banner release notes have consistent section headers
("New Features", "Resolved Issues", etc.). Detect these and chunk by section, similar to
how SOP chunking already works. Each chunk would then have a `section` metadata field.

**Overlapping parent-child chunks:** Index two resolutions: small chunks (200 chars) for precise
retrieval and larger chunks (1000 chars) for context in the prompt. Retrieve by small chunks,
send the parent chunk to the model. LlamaIndex calls this "sentence window retrieval".

---

## Streaming Responses (SSE)

The current API waits for the complete GPT response before returning (buffered). For answers that
take 3–8 seconds, streaming improves perceived latency significantly.

### Server-Sent Events (SSE) endpoint

```go
// POST /banner/ask/stream
func (h *Handler) BannerAskStream(c *gin.Context) {
    // ... embed + search (unchanged) ...

    c.Header("Content-Type", "text/event-stream")
    c.Header("Cache-Control", "no-cache")
    c.Header("X-Accel-Buffering", "no")

    tokenCh, err := h.pipeline.StreamAnswer(ctx, req, chunks)

    c.Stream(func(w io.Writer) bool {
        token, ok := <-tokenCh
        if !ok {
            fmt.Fprintf(w, "data: [DONE]\n\n")
            return false
        }
        fmt.Fprintf(w, "data: %s\n\n", token)
        return true
    })
}
```

Azure OpenAI supports streaming via `"stream": true` in the chat completion request. The response
is a sequence of `data: {"choices":[{"delta":{"content":"..."}}]}` SSE events.

**Frontend consumption:**
```javascript
const es = new EventSource('/banner/ask/stream', {method: 'POST', body: JSON.stringify(req)});
es.onmessage = e => { if (e.data !== '[DONE]') appendToken(e.data); };
```

**Note:** SSE is one-way (server → client). For full-duplex streaming, WebSockets would be needed —
but SSE is simpler and sufficient for this use case.

---

## Async Ingestion with Status Polling

Ingesting 100 pages takes ~1.7 minutes. The current API blocks the HTTP connection for the entire
duration. Long-running ingest requests time out behind proxies and load balancers (typically 30–60s).

### Design

```
POST /banner/ingest        → returns {"job_id": "abc-123"} immediately
GET  /jobs/abc-123         → {"status": "running", "progress": 42, "total": 180}
GET  /jobs/abc-123         → {"status": "done", "indexed": 180, "errors": 0}
```

### Implementation sketch

```go
// internal/jobs/store.go
type JobStatus struct {
    ID        string    `json:"job_id"`
    Status    string    `json:"status"`    // pending, running, done, failed
    Progress  int       `json:"progress"`
    Total     int       `json:"total"`
    Errors    int       `json:"errors"`
    StartedAt time.Time `json:"started_at"`
    DoneAt    time.Time `json:"done_at,omitempty"`
}

// In-memory store (or Redis for multi-instance)
type JobStore struct {
    mu   sync.RWMutex
    jobs map[string]*JobStatus
}
```

```go
// internal/api/handlers.go
func (h *Handler) BannerIngest(c *gin.Context) {
    jobID := uuid.NewString()
    h.jobs.Set(jobID, &jobs.JobStatus{Status: "pending"})

    go func() {
        h.jobs.Set(jobID, &jobs.JobStatus{Status: "running"})
        err := ingest.RunBanner(ctx, req, h.jobs.ProgressCallback(jobID))
        if err != nil {
            h.jobs.SetFailed(jobID, err)
        } else {
            h.jobs.SetDone(jobID)
        }
    }()

    c.JSON(http.StatusAccepted, gin.H{"job_id": jobID})
}
```

**Caveat:** In-memory job store is lost on restart. For production, use Redis or Azure Table Storage.
For a single-instance internal tool, in-memory is fine.

---

## Semantic Caching

Identical or near-identical questions get the same answer. Cache based on embedding similarity
rather than exact string match — "what changed in Banner Finance?" and "Banner Finance changes?"
should hit the same cache entry.

### Design

```
New question
     ↓
Embed question
     ↓
Search cache index (separate from knowledge index)
     ↓
If similarity > 0.97 → return cached answer (0ms LLM cost)
If not found → run full RAG pipeline → cache result
```

### Implementation

Use a second Azure AI Search index as the cache store. Each entry:
```json
{
  "id": "sha256-of-question",
  "question": "What changed in Banner Finance 9.3.22?",
  "embedding": [...],
  "answer": "...",
  "sources": [...],
  "cached_at": "2026-04-06T12:00:00Z",
  "ttl_hours": 168
}
```

On every ask: vector-search the cache index first. If a result comes back with score > threshold,
return it directly. Otherwise run the pipeline and write the result to the cache.

**TTL:** Cache entries should expire when new documents are ingested. Simplest: clear the cache
after every `/banner/ingest` run, or set a short TTL (24–48 hours).

**Cost savings:** Significant for monitoring dashboards and scheduled reports that ask the same
questions repeatedly.

---

## Document Lifecycle Management

### 1. Ingestion Timestamp Tracking

Each indexed chunk has a deterministic ID but no ingest timestamp. Add `ingested_at` to the chunk
document so you can answer: "when was this document last indexed?"

```go
// in the chunk document
"ingested_at": time.Now().UTC().Format(time.RFC3339),
```

Surface via `/index/stats` as `oldest_chunk` and `newest_chunk` fields.

---

### 2. Stale Document Detection

A scheduled job (ACA Job or Azure Function timer) checks if any document hasn't been re-indexed
in N days. Sends an alert if Banner release notes are older than 30 days — a proxy for "we forgot
to ingest the latest PDF".

---

### 3. Delete by Source

Currently there's no way to remove a specific document from the index without recreating the whole
index. Add a `DELETE /banner/document` endpoint that deletes all chunks matching a given source path.

```go
// DELETE /banner/document?source=banner/general/2026/february/Banner_General_9.3.37.2.pdf
// Deletes all chunks where source_path == that file
```

Azure AI Search supports batch deletes using the document ID. Since chunk IDs are deterministic
(MD5 of source + chunk index), the app can reconstruct the IDs without querying first.

---

## Developer Experience

### 1. Makefile

A single `Makefile` at the repo root eliminates the need to remember commands:

```makefile
.PHONY: run docs proto test build docker

run:
	go run cmd/main.go

run-grpc:
	go run cmd/grpc/main.go

docs:
	go generate ./internal/api/

proto:
	buf generate

test:
	go test ./internal/... -v -count=1

vet:
	go vet ./...

build:
	go build -o bin/omnivore-http ./cmd/main.go
	go build -o bin/omnivore-grpc ./cmd/grpc/main.go

docker:
	docker compose up --build

lint:
	golangci-lint run ./...
```

---

### 2. Air — Live Reload

[Air](https://github.com/air-verse/air) restarts the server automatically on file changes.
Essential for iterating on handler logic without `Ctrl-C && go run` loops.

```toml
# .air.toml
root = "."
cmd = "go run cmd/main.go"
include_ext = ["go"]
exclude_dir = ["docs", "gen", "data", "blog"]
```

```bash
go install github.com/air-verse/air@latest
air  # starts watching
```

---

### 3. GitHub Actions CI

```yaml
# .github/workflows/ci.yml
name: CI
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"
      - run: go vet ./...
      - run: go test ./internal/... -v
      - run: buf lint          # lint proto files
```

No Azure credentials needed in CI — the tests that exist (`ingest/`) don't hit Azure.

---

### 4. golangci-lint

A single linter runner that covers 50+ Go linters. Catches common mistakes the compiler misses:
`errcheck` (ignoring errors), `staticcheck` (dead code, incorrect API usage), `govet` (shadowing).

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
golangci-lint run ./...
```

A minimal `.golangci.yml` at the repo root configures which linters run.

---

### 5. Test Coverage for RAG Pipeline

Current test coverage: `ingest/` package (docx, sop chunking). Uncovered: `rag/`, `azure/`, `api/`.

**What to add without Azure mocks:**
- `rag/rag_test.go` — test prompt construction, mode routing logic, result ranking
- `rag/summarize_test.go` — test topic extraction from mock chunks
- `api/handlers_test.go` — test request validation (missing fields, invalid JSON)

**For Azure integration tests** (require real credentials):
- Tag them with `//go:build integration`
- Run only in CI when `AZURE_OPENAI_API_KEY` is set
- Use a dedicated test index to avoid polluting production data

---

## Priority Matrix

| Improvement | Impact | Effort | Priority |
|-------------|--------|--------|----------|
| API key auth middleware | High (security) | Low | **Do first** |
| Confidence score in response | High (enables routing) | Low | **Do first** |
| Structured logging (slog) | High (observability) | Low | **Do first** |
| Request ID middleware | Medium | Low | Do soon |
| CORS middleware | Medium | Low | Do soon |
| Makefile | Medium (DX) | Low | Do soon |
| Streaming SSE | Medium (UX) | Medium | Next sprint |
| Pagination | Medium | Low | Next sprint |
| Query rewriting | High (quality) | Medium | Next sprint |
| Async ingestion | Medium | Medium | Next sprint |
| Reranking (Azure semantic) | High (quality) | Medium | Next sprint |
| GitHub Actions CI | Medium | Low | Next sprint |
| HyDE | Medium (quality) | Medium | Experiment first |
| Multi-query retrieval | Medium (quality) | Medium | Experiment first |
| Semantic caching | Medium (cost) | High | Later |
| OpenTelemetry tracing | Low (for now) | High | Later |
| Answer grounding check | Medium (trust) | Medium | Later |
| Document delete endpoint | Low | Medium | Later |
| Chunking improvements | High (quality) | High | Later |
