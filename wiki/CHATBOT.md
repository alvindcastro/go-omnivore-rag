# Ask Banner — Botpress Chatbot over go-omnivore-rag

> **IDE:** GoLand (primary) — Go RAG backend + test suites  
> **Strict TDD:** Red → Green → Refactor on every coded component  
> **Goal:** Portfolio-grade, deployed chatbot demo for the AI/Automation Developer application  
> **Backend:** `go-omnivore-rag` — existing, live, no changes needed to core RAG

---

## Actual API Surface (from swagger.json)

The backend is already fully built. The Botpress adapter layer calls these endpoints.

### Banner endpoints (tag: `banner`)

| Method | Path | Purpose | Key request fields |
|---|---|---|---|
| `POST` | `/banner/ask` | Freeform Q&A over Banner release notes | `question` (required, min 5 chars), `module_filter`, `version_filter`, `year_filter`, `top_k` |
| `POST` | `/banner/summarize/actions` | Required actions for a release | `filename` (required), `banner_module`, `banner_version`, `top_k` |
| `POST` | `/banner/summarize/breaking` | Breaking changes in a release | same as above |
| `POST` | `/banner/summarize/changes` | What changed in a release | same as above |
| `POST` | `/banner/summarize/compatibility` | Compatibility notes | same as above |
| `POST` | `/banner/summarize/full` | Full summary across all topics | same as above → returns `FullSummaryResponse` |
| `POST` | `/banner/blob/sync` | Sync + ingest from Azure Blob Storage | `container_name`, `prefix`, `ingest_after_sync`, `overwrite` |
| `GET` | `/banner/blob/list` | List blobs | `?prefix=` query param |
| `POST` | `/banner/ingest` | Ingest PDFs into search index | `docs_path`, `overwrite`, `start_page`, `end_page` |

### SOP endpoints (tag: `sop`)

| Method | Path | Purpose | Key request fields |
|---|---|---|---|
| `POST` | `/sop/ask` | Freeform Q&A over SOPs | `question` (required, min 5 chars), `top_k` |
| `GET` | `/sop` | List all ingested SOPs | — |
| `POST` | `/sop/ingest` | Ingest SOP documents | `overwrite` |

### System endpoints (tag: `system`)

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health` | Health check |
| `GET` | `/index/stats` | Azure Search index stats |
| `POST` | `/index/create` | Create search index |
| `GET` | `/debug/chunks` | List up to 50 indexed chunks |

### Key response shapes

**`rag.AskResponse`** — returned by `/banner/ask` and `/sop/ask`:
```json
{
  "answer": "string",
  "question": "string",
  "retrieval_count": 3,
  "sources": [
    {
      "banner_module": "Student",
      "banner_version": "9.3.37",
      "chunk_text": "string",
      "document_title": "string",
      "filename": "string",
      "page": 12,
      "score": 0.87,
      "sop_number": "string",
      "source_type": "banner|sop",
      "year": "2024"
    }
  ]
}
```

**`rag.FullSummaryResponse`** — returned by `/banner/summarize/full`:
```json
{
  "action_items": "string",
  "banner_module": "string",
  "banner_version": "string",
  "breaking_changes": "string",
  "chunks_analyzed": 14,
  "compatibility": "string",
  "filename": "string",
  "source_pages": [1, 4, 7],
  "what_changed": "string"
}
```

---

## Architecture

```
[Botpress Cloud Widget]
        ↓  (student types message)
[Botpress Flow — Execute Code nodes]
        ↓  axios HTTP calls
[Botpress Adapter — new Go microservice]   ← only new code in this repo
        ├── POST /chat/ask        → routes to /banner/ask or /sop/ask
        ├── POST /chat/sentiment  → rule-based (new, TDD)
        ├── POST /chat/intent     → keyword classifier (new, TDD)
        └── POST /chat/summarize  → /banner/summarize/full
                ↓
[go-omnivore-rag backend — unchanged]
        ├── /banner/ask           (module_filter=Student|Finance|etc)
        ├── /sop/ask
        └── /banner/summarize/full
                ↓
[Azure OpenAI GPT-4o-mini + Azure AI Search]
```

**Key design decision:** The adapter is a thin translation layer only. Its job is to accept Botpress-shaped requests, route to the correct backend endpoint using intent, and derive `confidence` and `escalate` from the backend response (`sources[0].score`). No RAG logic lives here.

---

## Repository Structure

```
ask-banner/
├── PLANNING.md              ← this file
├── CLAUDE.md                ← GoLand AI assistant context
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── adapter/
│   │   ├── client.go        ← HTTP client wrapping go-omnivore-rag
│   │   └── client_test.go
│   ├── intent/
│   │   ├── classifier.go
│   │   └── classifier_test.go
│   └── sentiment/
│       ├── analyzer.go
│       └── analyzer_test.go
├── api/
│   ├── handlers.go          ← /chat/* HTTP handlers
│   └── handlers_test.go
├── demo/
│   └── index.html           ← Botpress widget embed (mock Drupal-style page)
└── botpress/
    └── flows/
        └── ask-banner.json  ← exported Botpress flow
```

---

## Phase 1 — Adapter Client (TDD)

> **CLAUDE.md rules that apply here:**
> - STRICT TDD: write the test file first, run it to confirm red, then implement
> - No external dependencies beyond stdlib + testify
> - All handlers accept injected interfaces — no concrete types in constructors
> - Handlers return structured JSON errors — never leak upstream error text
> - `go test ./... -v -race` must stay green throughout

---

### What it does

Wraps `/banner/ask` and `/sop/ask`. Derives `confidence` from `sources[0].score` (a 0–1 value
from Azure AI Search). Sets `escalate = true` when confidence < 0.5 or no results returned.

This is the only component that ever speaks to go-omnivore-rag. Everything above it (intent
classifier, HTTP handlers, Botpress) works through the `AdapterClient` interface — never directly
against the backend.

---

### Agents used in Phase 1

Two agents from [CLAUDE_AGENTS.md](CLAUDE_AGENTS.md) are active during this phase:

| Agent | Role in Phase 1 |
|-------|----------------|
| **Agent 7 — Index Health & Diagnostics** | Run before any live integration test. Confirms go-omnivore-rag is healthy, the index has data, and `/banner/ask` returns results. Catches environment problems before they look like adapter bugs. |
| **Agent 1 — Banner Ask Agent** | Post-implementation smoke test against the live backend. Sends the same questions the unit tests use and validates the real responses match expected shape and confidence range. |

**When to run Agent 7 (pre-flight):**

```python
# Before touching the live backend, run the diagnostics agent
python agents/diagnostics.py

# Expected output:
# ✓ Backend: healthy (gpt-4o-mini)
# ✓ Index: 1,247 chunks, 12 documents
# ✓ Test query top_score: 0.71 (above threshold)
# → System is ready for integration testing
```

If Agent 7 reports 0 chunks or backend unhealthy → fix the environment before writing
live-integration code. The unit tests (httptest mock) still run without a live backend.

**When to run Agent 1 (post-implementation smoke test):**

```python
# After unit tests pass, validate against the real backend
python agents/banner_ask.py "When is the add/drop deadline?"
# Expected: answer with confidence > 0.5, sources list non-empty, escalate = false

python agents/banner_ask.py "xyzzy nonsense question that matches nothing"
# Expected: escalate = true, confidence < 0.5
```

---

### Adapter response contract (what Botpress receives)

```json
{
  "answer": "The add/drop deadline for Winter 2025 is January 17th.",
  "confidence": 0.87,
  "sources": [
    { "title": "Banner Student 9.3.37 Release Notes", "page": 12, "source_type": "banner" }
  ],
  "escalate": false
}
```

**Mapping from `rag.AskResponse`:**

| `rag.AskResponse` field | → | `AdapterResponse` field | Notes |
|------------------------|---|------------------------|-------|
| `sources[0].score` | → | `confidence` | 0.0 if no sources |
| `retrieval_count == 0` OR `sources[0].score < 0.5` | → | `escalate = true` | Both conditions independently trigger |
| `sources[i].document_title` | → | `sources[i].title` | rename only |
| `sources[i].page` | → | `sources[i].page` | pass-through |
| `sources[i].sop_number` | → | `sources[i].sop_number` | empty string for banner source_type |
| `sources[i].source_type` | → | `sources[i].source_type` | "banner" or "sop" |

---

### File structure

```
internal/adapter/
├── client.go        ← implement this (Phase 1)
└── client_test.go   ← write this first, confirm red, then implement
```

**Step 1 (Red):** Create `client_test.go` with the tests below. Run:
```bash
go test ./internal/adapter/... -v
# → compilation errors (types don't exist yet) = expected RED state
```

**Step 2 (Green):** Implement `client.go` until all tests pass:
```bash
go test ./internal/adapter/... -v -race
# → PASS
```

**Step 3 (Refactor):** Run the full suite to confirm nothing else broke:
```bash
go test ./... -v -race
```

---

### TDD — `internal/adapter/client_test.go`

```go
func TestAdapterClient_BannerAsk_Success(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        assert.Equal(t, "/banner/ask", r.URL.Path)
        assert.Equal(t, http.MethodPost, r.Method)

        var req bannerAskRequest
        json.NewDecoder(r.Body).Decode(&req)
        assert.Equal(t, "When is the add/drop deadline?", req.Question)

        resp := ragAskResponse{
            Answer:         "The add/drop deadline is January 17th.",
            Question:       req.Question,
            RetrievalCount: 2,
            Sources: []ragSourceChunk{
                {Score: 0.87, DocumentTitle: "Banner Student 9.3.37", Page: 12, SourceType: "banner"},
            },
        }
        json.NewEncoder(w).Encode(resp)
    }))
    defer srv.Close()

    client := NewAdapterClient(srv.URL)
    result, err := client.AskBanner(context.Background(), "When is the add/drop deadline?", AskOptions{})

    require.NoError(t, err)
    assert.Equal(t, "The add/drop deadline is January 17th.", result.Answer)
    assert.InDelta(t, 0.87, result.Confidence, 0.01)
    assert.False(t, result.Escalate)
    assert.Len(t, result.Sources, 1)
}

func TestAdapterClient_BannerAsk_LowConfidence_SetsEscalate(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(ragAskResponse{
            Answer:         "I'm not sure.",
            RetrievalCount: 1,
            Sources:        []ragSourceChunk{{Score: 0.31}},
        })
    }))
    defer srv.Close()

    client := NewAdapterClient(srv.URL)
    result, err := client.AskBanner(context.Background(), "random question", AskOptions{})

    require.NoError(t, err)
    assert.True(t, result.Escalate)
}

func TestAdapterClient_BannerAsk_NoResults_Escalates(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(ragAskResponse{RetrievalCount: 0, Sources: []ragSourceChunk{}})
    }))
    defer srv.Close()

    client := NewAdapterClient(srv.URL)
    result, err := client.AskBanner(context.Background(), "anything", AskOptions{})

    require.NoError(t, err)
    assert.True(t, result.Escalate)
    assert.Zero(t, result.Confidence)
}

func TestAdapterClient_SopAsk_RoutesCorrectly(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        assert.Equal(t, "/sop/ask", r.URL.Path)
        json.NewEncoder(w).Encode(ragAskResponse{
            Answer:  "See SOP-42 for the procedure.",
            Sources: []ragSourceChunk{{Score: 0.91, SopNumber: "SOP-42", SourceType: "sop"}},
        })
    }))
    defer srv.Close()

    client := NewAdapterClient(srv.URL)
    result, err := client.AskSop(context.Background(), "How do I process a transcript request?")

    require.NoError(t, err)
    assert.Contains(t, result.Answer, "SOP-42")
    assert.Equal(t, "sop", result.Sources[0].SourceType)
}

func TestAdapterClient_WithModuleFilter(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var req bannerAskRequest
        json.NewDecoder(r.Body).Decode(&req)
        assert.Equal(t, "Student", req.ModuleFilter)
        json.NewEncoder(w).Encode(ragAskResponse{
            Answer:  "Registration opens on...",
            Sources: []ragSourceChunk{{Score: 0.78}},
        })
    }))
    defer srv.Close()

    client := NewAdapterClient(srv.URL)
    _, err := client.AskBanner(context.Background(), "When does registration open?",
        AskOptions{ModuleFilter: "Student"})

    require.NoError(t, err)
}
```

---

### GoLand prompt for Phase 1

```
Implement internal/adapter/client.go to pass all tests in client_test.go.

CLAUDE.md constraints:
- No external dependencies beyond stdlib + testify
- AdapterClient must be constructable via an interface — no concrete types in constructors
- go test ./... -v -race must pass

Types to define in client.go:

  // Internal — mirrors go-omnivore-rag JSON shape exactly
  type ragAskResponse struct {
      Answer         string          `json:"answer"`
      Question       string          `json:"question"`
      RetrievalCount int             `json:"retrieval_count"`
      Sources        []ragSourceChunk `json:"sources"`
  }

  type ragSourceChunk struct {
      Score         float64 `json:"score"`
      DocumentTitle string  `json:"document_title"`
      Page          int     `json:"page"`
      SopNumber     string  `json:"sop_number"`
      SourceType    string  `json:"source_type"`
  }

  // Request shapes sent to go-omnivore-rag
  type bannerAskRequest struct {
      Question     string `json:"question"`
      ModuleFilter string `json:"module_filter,omitempty"`
      VersionFilter string `json:"version_filter,omitempty"`
      YearFilter   string `json:"year_filter,omitempty"`
      TopK         int    `json:"top_k,omitempty"`
  }

  // Public — returned to callers (Botpress adapter contract)
  type AdapterResponse struct {
      Answer     string          `json:"answer"`
      Confidence float64         `json:"confidence"`
      Sources    []AdapterSource `json:"sources"`
      Escalate   bool            `json:"escalate"`
  }

  type AdapterSource struct {
      Title      string `json:"title"`
      Page       int    `json:"page"`
      SopNumber  string `json:"sop_number,omitempty"`
      SourceType string `json:"source_type"`
  }

  type AskOptions struct {
      ModuleFilter  string
      VersionFilter string
      YearFilter    string
      TopK          int
  }

AdapterClient implementation:
- NewAdapterClient(baseURL string) *AdapterClient
- func (c *AdapterClient) AskBanner(ctx context.Context, question string, opts AskOptions) (AdapterResponse, error)
  - POST to {baseURL}/banner/ask with bannerAskRequest
  - map ragAskResponse → AdapterResponse per the field table in CHATBOT.md
- func (c *AdapterClient) AskSop(ctx context.Context, question string) (AdapterResponse, error)
  - POST to {baseURL}/sop/ask with { "question": question }
  - same mapping

Confidence and escalation logic (must match CLAUDE.md contract exactly):
  Confidence = 0.0 if len(Sources) == 0
  Confidence = sources[0].Score otherwise
  Escalate   = (Confidence < 0.5) || (RetrievalCount == 0)

HTTP client:
  - Use http.NewRequestWithContext to honour ctx cancellation
  - Set Content-Type: application/json on every request
  - Default client timeout: 30s (set on the http.Client, not per-request)
  - On non-2xx status: return an error including the status code

TDD: make failing tests pass. No caching, retries, logging, or metrics.
```

---

### Implementation checklist

- [x] Create `internal/adapter/client_test.go` — copy tests above verbatim
- [x] Run `go test ./internal/adapter/... -v` → confirm compilation failure (RED)
- [ ] Run Agent 7 (diagnostics) if you plan to do live integration testing
- [x] Create `internal/adapter/client.go` with the types and methods above
- [x] Run `go test ./internal/adapter/... -v` → all 5 tests GREEN
- [x] Run `go test ./...` → confirm no regressions elsewhere
- [ ] Run Agent 1 (banner ask) against the live backend as a smoke test
- [ ] Proceed to Phase 2

> Note: `-race` flag requires CGO_ENABLED=1 on this machine. Run with CGO enabled when available.

---

### Live integration smoke test (post-implementation)

Once unit tests pass, validate against the real backend. Use Agent 1 from
[CLAUDE_AGENTS.md](CLAUDE_AGENTS.md) or run these curl commands:

```bash
# Healthy, high-confidence result — Escalate should be false
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question": "What is the add/drop deadline?", "module_filter": "Student", "top_k": 3}' \
  | jq '{answer: .answer, score: .sources[0].score, retrieval_count: .retrieval_count}'

# Expected: score > 0.5, retrieval_count > 0

# Low-confidence result — Escalate should be true in the adapter layer
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question": "xyzzy placeholder nothing matches this", "top_k": 3}' \
  | jq '{score: .sources[0].score, retrieval_count: .retrieval_count}'

# Expected: score < 0.5 or retrieval_count == 0
```

If the backend returns unexpected shapes here, Agent 7 (diagnostics) will identify
whether the problem is the index, the model deployment, or the request format.

---

## Phase 2 — Sentiment Pre-Filter (TDD)

### What it does
Classifies the student's message as `Positive`, `Neutral`, or `Frustrated` before it hits RAG. Frustrated messages (score > 0.7) bypass the RAG path entirely and route to human escalation in Botpress.

### TDD — `internal/sentiment/analyzer_test.go`

```go
func TestAnalyzer_FrustratedMessages(t *testing.T) {
    a := NewAnalyzer(DefaultConfig())
    cases := []struct {
        input    string
        expected Sentiment
    }{
        {"WHY IS THIS NOT WORKING???", Frustrated},
        {"I've been waiting 3 days and nobody answers", Frustrated},
        {"This system is absolutely useless!!", Frustrated},
        {"I keep getting an error and this is ridiculous", Frustrated},
    }
    for _, c := range cases {
        result := a.Analyze(c.input)
        assert.Equal(t, Frustrated, result.Sentiment, "input: %q", c.input)
        assert.Greater(t, result.Score, 0.6, "input: %q", c.input)
    }
}

func TestAnalyzer_NeutralMessages(t *testing.T) {
    a := NewAnalyzer(DefaultConfig())
    for _, msg := range []string{
        "When is the add/drop deadline?",
        "How do I pay my fees?",
        "I need a transcript",
    } {
        result := a.Analyze(msg)
        assert.Equal(t, Neutral, result.Sentiment, "input: %q", msg)
    }
}

func TestAnalyzer_PositiveMessages(t *testing.T) {
    a := NewAnalyzer(DefaultConfig())
    assert.Equal(t, Positive, a.Analyze("Thank you that was really helpful!").Sentiment)
}

func TestAnalyzer_ScoreInRange(t *testing.T) {
    a := NewAnalyzer(DefaultConfig())
    for _, msg := range []string{"hello", "HELP ME NOW!!!", "thanks"} {
        r := a.Analyze(msg)
        assert.GreaterOrEqual(t, r.Score, 0.0)
        assert.LessOrEqual(t, r.Score, 1.0)
    }
}

func TestAnalyzer_CustomConfig(t *testing.T) {
    cfg := Config{
        FrustratedKeywords: []string{"banana"},
        PositiveKeywords:   []string{"mango"},
    }
    a := NewAnalyzer(cfg)
    assert.Equal(t, Frustrated, a.Analyze("this banana situation").Sentiment)
    assert.Equal(t, Positive, a.Analyze("I love mango").Sentiment)
}
```

### GoLand prompt for Phase 2

```
Implement internal/sentiment/analyzer.go to pass analyzer_test.go.

Requirements:
- Sentiment enum: Positive, Neutral, Frustrated
- SentimentResult struct: { Sentiment Sentiment; Score float64 }
- Analyzer.Analyze(message string) → SentimentResult
- Accept Config{ FrustratedKeywords []string; PositiveKeywords []string }
  so tests can inject custom word lists without DefaultConfig
- DefaultConfig() returns:
    FrustratedKeywords: ["useless", "ridiculous", "not working", "nobody", "waiting too long",
                         "broken", "error", "can't", "doesn't work", "terrible"]
    PositiveKeywords:   ["thank", "thanks", "helpful", "great", "perfect", "works"]
- Scoring heuristics (combine, normalize to 0–1):
    1. All-caps ratio: ALL_CAPS words / total words
    2. Punctuation density: (! + ? count) / message length
    3. Keyword match weight: each frustrated keyword match += 0.25, capped at 1.0
- Frustration wins over Positive when both match
- Neutral = no keyword hits and low punctuation/caps scores

TDD: only what makes failing tests pass.
```

---

## Phase 3 — Intent Classification (TDD)

### 6 intents mapped to backend routes

| Intent | Backend call | module_filter | Example questions |
|---|---|---|---|
| `RegistrationBanner` | `/banner/ask` | `Student` | "add/drop deadline", "how to register", "waitlist" |
| `FinanceBanner` | `/banner/ask` | `Finance` | "fee due date", "how to pay tuition", "fee deferral" |
| `TranscriptSop` | `/sop/ask` | — | "request a transcript", "official transcript" |
| `HoldsSop` | `/sop/ask` | — | "hold on my account", "clear a hold" |
| `ReleaseSummary` | `/banner/summarize/full` | — | "what changed in 9.3.37", "breaking changes", "release notes" |
| `General` | `/banner/ask` | none | everything else |

### TDD — `internal/intent/classifier_test.go`

```go
func TestClassifier_KnownIntents(t *testing.T) {
    c := NewClassifier(DefaultIntentConfig())
    cases := []struct {
        input    string
        expected Intent
    }{
        {"When is the add/drop deadline?", RegistrationBanner},
        {"How do I register for next semester?", RegistrationBanner},
        {"When are my tuition fees due?", FinanceBanner},
        {"How do I request an official transcript?", TranscriptSop},
        {"There is a financial hold on my account", HoldsSop},
        {"What changed in Banner General 9.3.37?", ReleaseSummary},
        {"What is the weather today?", General},
        {"help", General},
    }
    for _, tc := range cases {
        result := c.Classify(tc.input)
        assert.Equal(t, tc.expected, result.Intent, "input: %q", tc.input)
    }
}

func TestClassifier_ConfidenceRange(t *testing.T) {
    c := NewClassifier(DefaultIntentConfig())
    for _, msg := range []string{"register for COMP 1234", "pay my fees", "transcript request"} {
        r := c.Classify(msg)
        assert.GreaterOrEqual(t, r.Confidence, 0.0)
        assert.LessOrEqual(t, r.Confidence, 1.0)
    }
}

func TestClassifier_AmbiguousDefaultsToGeneral(t *testing.T) {
    c := NewClassifier(DefaultIntentConfig())
    r := c.Classify("I have a question")
    assert.Equal(t, General, r.Intent)
    assert.Less(t, r.Confidence, 0.3)
}

func TestClassifier_CustomConfig(t *testing.T) {
    cfg := IntentConfig{
        RegistrationBanner: []string{"enroll"},
    }
    c := NewClassifier(cfg)
    assert.Equal(t, RegistrationBanner, c.Classify("how do I enroll?").Intent)
}

func TestClassifier_ReleaseSummaryDetectsVersion(t *testing.T) {
    c := NewClassifier(DefaultIntentConfig())
    for _, msg := range []string{
        "What changed in 9.3.37?",
        "show me the breaking changes for Banner",
        "release notes for Student 9.4",
    } {
        assert.Equal(t, ReleaseSummary, c.Classify(msg).Intent, "input: %q", msg)
    }
}
```

### GoLand prompt for Phase 3

```
Implement internal/intent/classifier.go to pass classifier_test.go.

Requirements:
- Intent enum: RegistrationBanner, FinanceBanner, TranscriptSop, HoldsSop, ReleaseSummary, General
- IntentResult struct: { Intent Intent; Confidence float64 }
- Classifier.Classify(message string) → IntentResult
- Accept IntentConfig (map of Intent → []string keywords) for testability
- DefaultIntentConfig() returns:
    RegistrationBanner: ["register", "registration", "add/drop", "add drop", "waitlist", "enroll", "course selection"]
    FinanceBanner:      ["fee", "fees", "tuition", "pay", "payment", "deferral", "invoice", "balance owing"]
    TranscriptSop:      ["transcript", "official transcript", "unofficial transcript", "academic record"]
    HoldsSop:           ["hold", "holds", "financial hold", "registration hold", "clear hold"]
    ReleaseSummary:     ["what changed", "breaking changes", "release notes", "release", "version", "9.", "compatibility"]
    General:            [] (fallback — no keywords)
- Scoring: each keyword match adds weight; longer phrase = higher weight;
  normalize winning score to 0–1
- General fallback when no intent scores >= 0.3
- Case-insensitive matching

TDD: only what makes failing tests pass.
```

---

## Phase 4 — HTTP Handlers (TDD)

### Adapter endpoints exposed to Botpress

| Method | Path | Calls | Purpose |
|---|---|---|---|
| `POST` | `/chat/ask` | `AskBanner` or `AskSop` based on intent | Main Q&A path |
| `POST` | `/chat/sentiment` | `Analyzer.Analyze` | Pre-filter check |
| `POST` | `/chat/intent` | `Classifier.Classify` | Intent detection |
| `GET` | `/health` | — | Health check |

### `/chat/ask` request / response

```json
// Request
{
  "message": "When is the add/drop deadline?",
  "session_id": "botpress-abc123",
  "intent": "RegistrationBanner"
}

// 200 Response
{
  "answer": "The add/drop deadline for Winter 2025 is January 17th.",
  "confidence": 0.87,
  "sources": [
    { "title": "Banner Student 9.3.37", "page": 12, "source_type": "banner" }
  ],
  "escalate": false
}
```

### TDD — `api/handlers_test.go`

```go
func TestChatAskHandler_ValidBannerIntent(t *testing.T) {
    mockClient := &mockAdapterClient{
        askBannerFn: func(ctx context.Context, q string, opts AskOptions) (AdapterResponse, error) {
            assert.Equal(t, "Student", opts.ModuleFilter)
            return AdapterResponse{
                Answer:     "Registration opens March 1st.",
                Confidence: 0.83,
                Sources:    []AdapterSource{{Title: "Banner Student 9.3.37", Page: 5}},
                Escalate:   false,
            }, nil
        },
    }

    body := `{"message":"When does registration open?","session_id":"s1","intent":"RegistrationBanner"}`
    req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    NewChatHandler(mockClient).ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
    var resp chatAskResponse
    json.Unmarshal(w.Body.Bytes(), &resp)
    assert.False(t, resp.Escalate)
    assert.InDelta(t, 0.83, resp.Confidence, 0.01)
}

func TestChatAskHandler_SopIntent_CallsAskSop(t *testing.T) {
    mockClient := &mockAdapterClient{
        askSopFn: func(ctx context.Context, q string) (AdapterResponse, error) {
            return AdapterResponse{Answer: "See SOP-42.", Confidence: 0.91}, nil
        },
    }

    body := `{"message":"How do I request a transcript?","session_id":"s2","intent":"TranscriptSop"}`
    req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    NewChatHandler(mockClient).ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
}

func TestChatAskHandler_MissingMessage_Returns400(t *testing.T) {
    body := `{"session_id":"s3","intent":"General"}`
    req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    NewChatHandler(&mockAdapterClient{}).ServeHTTP(w, req)

    assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestChatAskHandler_BackendError_Returns500_WithSafeMessage(t *testing.T) {
    mockClient := &mockAdapterClient{
        askBannerFn: func(_ context.Context, _ string, _ AskOptions) (AdapterResponse, error) {
            return AdapterResponse{}, errors.New("upstream timeout: connection refused")
        },
    }

    body := `{"message":"When does registration open?","session_id":"s4","intent":"RegistrationBanner"}`
    req := httptest.NewRequest(http.MethodPost, "/chat/ask", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    NewChatHandler(mockClient).ServeHTTP(w, req)

    assert.Equal(t, http.StatusInternalServerError, w.Code)
    var errBody map[string]string
    json.Unmarshal(w.Body.Bytes(), &errBody)
    // Must not leak upstream error details
    assert.Equal(t, "internal server error", errBody["error"])
    assert.NotContains(t, w.Body.String(), "connection refused")
}
```

### GoLand prompt for Phase 4

```
Implement api/handlers.go to pass handlers_test.go.

Context:
- ChatHandler wraps an AdapterClient interface (not the concrete type) for testability
- POST /chat/ask:
    Validate: message non-empty (return 400), session_id non-empty (return 400)
    Route by intent field:
        "RegistrationBanner" → AskBanner(ctx, msg, AskOptions{ModuleFilter: "Student"})
        "FinanceBanner"      → AskBanner(ctx, msg, AskOptions{ModuleFilter: "Finance"})
        "TranscriptSop"      → AskSop(ctx, msg)
        "HoldsSop"           → AskSop(ctx, msg)
        default / "General"  → AskBanner(ctx, msg, AskOptions{})
    On error: return 500 with {"error": "internal server error"} — never leak upstream text
    On success: return AdapterResponse as JSON
- GET /health: return {"status": "ok"}

AdapterClient interface (define alongside handlers):
    AskBanner(ctx context.Context, question string, opts AskOptions) (AdapterResponse, error)
    AskSop(ctx context.Context, question string) (AdapterResponse, error)

TDD: make failing tests pass only.
```

---

## Phase 5 — Botpress Flow Design

### Flow map

```
[Start]
    ↓
[Capture message]
    ↓
[Execute Code: POST /chat/sentiment]
    ↓  score > 0.7 (Frustrated)?
    ├── YES → "I can see you're frustrated. Let me connect you with someone."
    │          → Escalation card (registrar email / phone / hours)
    │          → [End]
    └── NO ↓
[Execute Code: POST /chat/intent]
    ↓
[Intent router]
    ├── RegistrationBanner → optional slot: "Which term?" 
    ├── FinanceBanner       → optional slot: "Which fee type?"
    ├── TranscriptSop       → go directly
    ├── HoldsSop            → go directly
    ├── ReleaseSummary      → slot: "Which Banner module/version?" (required for /summarize/full)
    └── General             → go directly
    ↓
[Execute Code: POST /chat/ask — intent pre-filled]
    ↓  escalate = true?
    ├── YES → "I don't have a confident answer for that. Here's who can help: [contact]"
    └── NO  → Display answer + source citations (title + page)
                ↓
            [👍 / 👎 feedback buttons]
                ↓
            [End / restart loop]
```

### Botpress Execute Code snippets

**Sentiment check:**
```javascript
const axios = require('axios');
const RAG = process.env.RAG_ADAPTER_URL;

const r = await axios.post(`${RAG}/chat/sentiment`, { message: event.preview });
workflow.sentimentScore = r.data.score;
workflow.sentiment = r.data.sentiment; // "Positive" | "Neutral" | "Frustrated"
```

**Intent detection:**
```javascript
const r = await axios.post(`${RAG}/chat/intent`, { message: event.preview });
workflow.intent = r.data.intent;             // "RegistrationBanner" | "FinanceBanner" | ...
workflow.intentConfidence = r.data.confidence;
```

**Main ask:**
```javascript
const r = await axios.post(`${RAG}/chat/ask`, {
  message: event.preview,
  session_id: event.botId + '-' + event.userId,
  intent: workflow.intent || 'General'
});

workflow.answer    = r.data.answer;
workflow.confidence = r.data.confidence;
workflow.escalate  = r.data.escalate;
workflow.sources   = r.data.sources; // rendered as citation cards
```

**Environment variable in Botpress:**
```
RAG_ADAPTER_URL=https://ask-banner.fly.dev   # or ngrok URL for local dev
```

---

## Phase 6 — Demo Page

### `demo/index.html`
- Mock college header — brand colours `#003366` / `#0066CC`
- Page title: "Banner Admin Assistant" *(updated from "Student Self-Service" to target IT/admin persona)*
- Nav links: Upgrades | Server SOPs | Version Info | Contact
- Visible callout: "Ask our virtual assistant 24/7 — powered by Banner documentation"
- Three topic cards: Banner Upgrades, Server Restart SOP, Banner Version Info
- "Try asking" chip row with sample questions
- Botpress widget `<script>` tag at bottom of body

### Implementation checklist

- [x] Create `demo/index.html` with brand colours `#003366` / `#0066CC`
- [x] Admin-persona nav: Upgrades | Server SOPs | Version Info | Contact
- [x] Hero callout present
- [x] Cards: Banner Upgrades, Server Restart SOP, Banner Version Info, Contact & Escalation
- [x] "Try asking" chip row — Banner upgrade, SOP restart, version number, breaking changes, module impact
- [x] Botpress `<script>` embed with placeholder `YOUR_BOT_ID`
- [x] Replace `YOUR_BOT_ID` with real Botpress Cloud shareable bot ID (`3b6cf557-bc0a-4197-b16a-29c79706809f`)
- [ ] Set `RAG_ADAPTER_URL` env var in Botpress Cloud (ngrok or Fly.io)
- [ ] Record Loom demo using script below

### Loom recording script (3 min)

| Time | Action |
|---|---|
| 0:00 | Show demo page: "Student-facing assistant built on our Banner RAG backend" |
| 0:30 | Type "When is the add/drop deadline?" → grounded answer with source title + page |
| 1:00 | Type "How do I request a transcript?" → SOP routing path |
| 1:30 | Type "I've been waiting 3 days and nobody answers me!!!" → frustrated escalation |
| 2:00 | Type something obscure → low-confidence `escalate=true` path |
| 2:30 | Flip to GoLand → run `go test ./... -v -race` → all green |

---

## Stretch Goals

| Feature | JD duty mapping |
|---|---|
| Wire `/banner/summarize/full` as dedicated flow path (slot: module + version) | Deeper Banner integration showcase |
| Multi-turn: pass `conversation_history` to `/chat/ask` | Conversational state management |
| HuggingFace zero-shot fallback when keyword score < 0.3 | Duty 4: "trains, fine-tunes, deploys ML models" |
| Confidence logging → Streamlit drift dashboard | Duty 6: monitor, evaluate, retrain |
| Dockerfile + deploy to Fly.io free tier | "Cloud-based AI deployments" |
| Real `/sop` list endpoint powering a "Browse SOPs" flow node | Shows full API surface integration |

---

## CLAUDE.md (drop in repo root for GoLand sessions)

```markdown
# Ask Banner — GoLand AI Context

## What this is
Thin Botpress adapter over the go-omnivore-rag backend.
Student-facing Tier 0 chatbot for Banner ERP Q&A.

## Backend API (go-omnivore-rag — do not modify)
POST /banner/ask       { question (required), module_filter?, version_filter?, year_filter?, top_k? }
POST /sop/ask          { question (required), top_k? }
POST /banner/summarize/full  { filename (required), banner_module?, banner_version?, top_k? }
Returns rag.AskResponse:  { answer, question, retrieval_count, sources[] }
sources[0].score = confidence value (0–1, from Azure AI Search)

## This repo — new code only
internal/adapter   → HTTP client wrapping go-omnivore-rag
internal/intent    → keyword intent classifier (6 intents)
internal/sentiment → rule-based sentiment analyzer
api/handlers       → /chat/* endpoints consumed by Botpress

## Non-negotiable rules
- STRICT TDD: test first (red), then implement (green), then refactor
- No external dependencies beyond stdlib + testify
- Handlers return structured JSON errors — never leak upstream error messages
- Confidence = sources[0].score; Escalate = true when confidence < 0.5 or retrieval_count == 0
- All handlers accept injected interfaces — no concrete types in constructors

## Test runner
go test ./... -v -race

## Intent → backend routing table
RegistrationBanner → /banner/ask  module_filter=Student
FinanceBanner      → /banner/ask  module_filter=Finance
TranscriptSop      → /sop/ask
HoldsSop           → /sop/ask
ReleaseSummary     → /banner/summarize/full
General            → /banner/ask  (no filter)
```
