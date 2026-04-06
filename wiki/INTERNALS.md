# Developer Internals

Deep-dive reference for anyone reading, extending, or debugging this codebase.
Covers *why* decisions were made, *how* things work internally, and *when* things will break.

---

## Table of Contents

1. [Design Philosophy](#design-philosophy)
2. [Why Go?](#why-go)
3. [Package Responsibilities](#package-responsibilities)
4. [Data Flow: Ingestion Pipeline](#data-flow-ingestion-pipeline)
5. [Data Flow: RAG Query Pipeline](#data-flow-rag-query-pipeline)
   - [Auto-routing mode](#auto-routing-mode)
   - [Module-scoped routing](#module-scoped-routing)
   - [Student User Guide Pipelines](#student-user-guide-pipelines)
6. [Chunking Strategies](#chunking-strategies)
7. [Azure Search Index Design](#azure-search-index-design)
8. [Why Direct HTTP (No Azure Go SDK)?](#why-direct-http-no-azure-go-sdk)
9. [DOCX Parsing Without External Libraries](#docx-parsing-without-external-libraries)
10. [Metadata Extraction from File Paths](#metadata-extraction-from-file-paths)
11. [Deterministic Chunk IDs](#deterministic-chunk-ids)
12. [Prompt Construction](#prompt-construction)
13. [Rate Limit Handling](#rate-limit-handling)
14. [Config: Fail Fast vs. Graceful](#config-fail-fast-vs-graceful)
15. [gRPC: Why It Exists and Its Current State](#grpc-why-it-exists-and-its-current-state)
16. [Known Limitations and Gotchas](#known-limitations-and-gotchas)
17. [Where Things Can Go Wrong](#where-things-can-go-wrong)
18. [How to Extend](#how-to-extend)
19. [Testing Gaps](#testing-gaps)
20. [Performance Notes](#performance-notes)

---

## Design Philosophy

The core tension in this codebase is **simplicity vs. capability**. Every major decision resolves
that tension toward simplicity:

- **No middleware stack** — auth, rate limiting, CORS are all TODO
- **No ORM** — Azure Search documents are plain structs marshalled to JSON
- **No DI framework** — dependencies are passed explicitly
- **No background workers** — ingestion is synchronous and request-scoped
- **No caching** — every question hits Azure OpenAI and Azure Search live

This is intentional for an educational codebase. Real production would add all of the above.
The tradeoff: easy to trace a request end-to-end in 20 minutes of reading.

---

## Why Go?

The language choice matters for how the code is structured.

**What it buys:**
- Explicit error handling — you see every place where an Azure call can fail
- No implicit magic — no ORM, no reflection-heavy DI, no annotation processing
- Single binary deployment — `go build` produces one file
- Fast startup — no JVM/Python interpreter warmup
- `encoding/xml` and `archive/zip` in stdlib — DOCX parsing with zero dependencies

**What it costs:**
- Verbose JSON marshalling (no automatic struct-to-JSON like Python dataclasses)
- No native async/await — concurrency requires goroutines + channels (not used here yet)
- Swagger annotations are comment-based which is fragile and verbose

---

## Package Responsibilities

```
config/          → Loads env vars once. No logic. Panics if required vars are absent.
internal/azure/  → Thin HTTP clients for OpenAI, Search, Blob. Each file = one Azure service.
internal/ingest/ → Document pipeline: walk files → extract text → chunk → embed → upload.
internal/rag/    → Query pipeline: embed question → hybrid search → build prompt → chat.
internal/api/    → HTTP handlers and Gin router. Handlers are thin — delegate to rag/ingest.
internal/grpcserver/ → gRPC handlers. Scaffolded, not fully implemented.
cmd/             → Entry points only. Wire config → dependencies → server start.
proto/           → Service contracts. Source of truth for gRPC interface.
```

**Dependency direction** (strict, no cycles):

```
cmd → internal/api → internal/rag → internal/azure
                   → internal/ingest → internal/azure
                                     → internal/azure/openai (for embedding)
config ← everything (leaf dependency, imports nothing internal)
```

If you add a dependency that goes the other direction (e.g., `azure` importing `ingest`), you've
introduced a cycle and it won't compile.

---

## Data Flow: Ingestion Pipeline

Entry: `POST /banner/ingest` or `POST /sop/ingest`
Handler: `internal/api/handlers.go` → `BannerIngest()` or `SopIngest()`
Core logic: `internal/ingest/ingest.go` → `Run()`

```
Run(docsPath, overwrite, pagesPerBatch, startPage, endPage)
│
├── If overwrite=true → search.CreateIndex()
│   (deletes and recreates the entire Azure Search index)
│
├── filepath.WalkDir(docsPath)
│   For each file:
│
│   ┌── isSopDocument(path)?  [checks if path contains "/sop/" or "\sop\"]
│   │
│   ├── YES → ingestSopFile()
│   │   ├── parseSopFilename()          [regex: "SOP(\d+) - (.+)\.docx"]
│   │   ├── extractDocxParagraphs()     [ZIP → word/document.xml → token XML decode]
│   │   ├── chunkSop()                  [section-aware, heading-triggered boundaries]
│   │   └── For each SopChunk:
│   │       ├── openai.EmbedText(chunk.Text)     [POST to ada-002 deployment]
│   │       ├── build ChunkDocument{}
│   │       └── search.UploadDocuments([doc])    [POST to Azure Search]
│   │
│   └── NO → ingestFile()
│       ├── parseMetadata(filePath)     [extract module/version/year from path]
│       ├── For pages in [startPage, endPage] batched by pagesPerBatch:
│       │   ├── extractPDFPages() / extractTextFile() / readMarkdown()
│       │   ├── chunkText(page, chunkSize, overlap)
│       │   ├── sanitizeText(chunk)
│       │   └── For each chunk (batched 100):
│       │       ├── openai.EmbedText(chunk)
│       │       ├── build ChunkDocument{}
│       │       └── search.UploadDocuments(batch)
│       └── 500ms sleep between embedding calls (rate limit preemption)
```

**Key file references:**
- Walk + dispatch: `internal/ingest/ingest.go:47–120`
- Banner chunk loop: `internal/ingest/ingest.go:144–225`
- SOP chunk loop: `internal/ingest/ingest.go:228–278`
- Text chunking: `internal/ingest/ingest.go:283–325`
- Metadata parsing: `internal/ingest/ingest.go:328–377`
- Chunk ID generation: `internal/ingest/ingest.go:379–383`

---

## Data Flow: RAG Query Pipeline

Entry: `POST /banner/ask`, `POST /banner/{module}/ask`, or `POST /sop/ask`
Handler: `internal/api/handlers.go` → `BannerAsk()`, `ModuleAsk()`, or `SopAsk()`
Core logic: `internal/rag/rag.go` → `Ask()`

```
Ask(AskRequest{Mode, ModuleFilter, SourceTypeFilter, SectionFilter, ...})
│
├── resolve module → ModuleDef{SystemPrompt, SearchPrefix}
│
├── switch Mode:
│   ├── "local"   (default) → askWithPrompt()
│   ├── "web"               → askWeb()
│   ├── "hybrid"            → askHybrid()
│   └── "auto"              → askAuto()  [selects mode based on confidence]
│
└── askWithPrompt() — local path:
    ├── Step 1: openai.EmbedText(request.Question)
    │   → 1536-dimensional float32 vector
    │
    ├── Step 2: search.HybridSearch(queryText, queryVector, filters...)
    │   → Azure Search combines:
    │       - BM25 keyword match on "chunk_text" + "document_title"
    │       - HNSW vector similarity on "content_vector" (cosine)
    │       - OData filters: source_type, banner_version, banner_module, year, section
    │   → Returns top-K SearchResult
    │
    ├── Step 3: Map SearchResult → SourceChunk (public-facing type)
    │
    ├── Step 4: buildPrompt(question, chunks)
    │   → Numbered list of source excerpts with metadata headers
    │   → System message instructs GPT to cite [1], [2], etc.
    │
    └── Step 5: openai.ChatComplete(systemPrompt, userPrompt)
        → temperature: 0.1  (factual, low creativity)
        → max_tokens: 800
        → Returns answer string
```

### Auto-routing mode

`mode="auto"` runs a local search first, then decides the retrieval strategy from the top-score:

```
score >= CONFIDENCE_HIGH_THRESHOLD  → local only   (strong match)
CONFIDENCE_LOW_THRESHOLD <= score < HIGH → hybrid  (supplement with Tavily)
score < CONFIDENCE_LOW_THRESHOLD    → web only     (weak local signal)
0 results                           → web only
Tavily unavailable (no API key)     → always fall back to local
```

Thresholds are configured via `CONFIDENCE_HIGH_THRESHOLD` and `CONFIDENCE_LOW_THRESHOLD` env vars.
The `RoutingDecision` object is included in the response when mode is `"auto"`.

### Module-scoped routing

`POST /banner/{module}/ask` extracts the module name from the URL path (e.g., `/banner/finance/ask`
sets `ModuleFilter="finance"`). The handler uses `ModuleAsk()` which resolves a `ModuleDef` —
a per-module system prompt and Tavily search prefix — so the LLM persona matches the module domain.

**Key file references:**
- `Ask()` and mode dispatch: `internal/rag/rag.go:160–178`
- `askAuto()`: `internal/rag/rag.go:194–`
- `buildPrompt()`: `internal/rag/rag.go:151–183`
- `HybridSearch()`: `internal/azure/search.go:95–185`
- `ChatComplete()`: `internal/azure/openai.go:55–130`
- `EmbedText()`: `internal/azure/openai.go:133–190`
- Module definitions: `internal/rag/rag.go` — `resolveModule()`

### Student User Guide Pipelines

Three dedicated pipelines in `internal/rag/student.go` handle the Student user guide:

| Endpoint | Handler | Pipeline method | Description |
|----------|---------|----------------|-------------|
| `POST /banner/student/ask` | `StudentAsk` | `Ask()` with `SourceTypeBannerGuide` | General Q&A against the user guide |
| `POST /banner/student/procedure` | `StudentProcedure` | `StudentProcedure()` | Step-by-step task instructions |
| `POST /banner/student/lookup` | `StudentLookup` | `StudentLookup()` | Concept/term definition |
| `POST /banner/student/cross-reference` | `StudentCrossReference` | `StudentCrossReference()` | Impact analysis across release notes + guide |

`StudentCrossReference` runs two parallel searches — one against `source_type=banner` (release notes)
and one against `source_type=banner_user_guide` — then merges the context into a single GPT prompt.
The response includes separate `release_sources` and `guide_sources` arrays.

---

## Chunking Strategies

Two separate strategies exist because the document types have fundamentally different structure.

### Banner PDFs: Character-Based with Overlap

**Why:** PDFs have no semantic structure (no headings in the extracted text). Pages are the only
natural boundary. Within a page, sentence boundaries are the best we can detect without an NLP library.

**How:**
1. Extract text page-by-page using `github.com/ledongthuc/pdf`
2. Split into chunks of `CHUNK_SIZE` characters (default: 500)
3. Overlap of `CHUNK_OVERLAP` chars (default: 25) ensures context isn't cut mid-sentence
4. Prefer breaking on `\n\n` > `\n` > `. ` > ` ` > hard-cut
5. `sanitizeText()` strips unicode junk, normalizes whitespace, drops micro-fragments (<50 chars)

**Trade-off:** Character-based means a chunk might end mid-sentence if no clean boundary is found
within `CHUNK_SIZE`. The overlap mitigates this but doesn't eliminate it.

**Code:** `internal/ingest/ingest.go:283–325`

### SOP DOCX: Section-Aware with Breadcrumbs

**Why:** SOPs have hierarchical structure (headings and numbered sections). Respecting that structure
keeps each chunk semantically self-contained and improves retrieval precision.

**How:**
1. Parse paragraphs from `word/document.xml` with their Word styles (Heading1–4, Normal, ListParagraph)
2. Strip cover page content (company name, change history, table of contents) using heuristics
3. When a heading-level paragraph is encountered, the current chunk closes and a new one opens
4. Every chunk is prefixed with a breadcrumb: `[SOP 154 — Procedure - Start, Stop Axiom] > 6. Detailed Procedures > 6.2 Stopping Axiom`
5. The breadcrumb is included in the text that gets embedded, so vector similarity carries section context

**Two heading detection modes:**
- **Styled:** Word paragraph style is `Heading1` / `Heading2` / `Heading3` / `Heading4`
- **Numbered:** `Normal` paragraph whose text matches `^\d+(\.\d+)*\s+\S` (e.g., `6.2 Stopping Axiom`)

**Why breadcrumbs matter:** When GPT-4o-mini receives a retrieved chunk, it has no idea which SOP it
came from unless we embed that in the chunk text. Breadcrumbs solve this without requiring the LLM
to infer it from metadata alone.

**Code:** `internal/ingest/sop_chunker.go`

---

## Azure Search Index Design

### Why a single index with `source_type` filtering?

Alternative: separate indexes for Banner and SOP.

**Pros of separate indexes:** Cleaner namespace, can have different schemas per doc type.
**Cons:** Twice the index management overhead; the schemas are actually 99% identical (both have
chunk_text, content_vector, filename, metadata fields). The `source_type` filter on every query
achieves the same isolation with zero additional infrastructure.

**Verdict:** Single index is correct for this scale. Revisit if doc types diverge structurally.

### HNSW Parameters

```json
"hnswParameters": {
  "metric": "cosine",
  "m": 4,
  "efConstruction": 400,
  "efSearch": 500
}
```

- `m: 4` — number of bidirectional links per node. Lower = smaller index, slightly less accurate.
  Default in most libraries is 16. Set to 4 here because the index is small (thousands of chunks).
- `efConstruction: 400` — build-time search width. Higher = better index quality, slower ingestion.
- `efSearch: 500` — query-time search width. Higher = better recall, slower queries.

For a knowledge base of this size, these settings are fine. At millions of documents, lower `m` will
hurt recall noticeably.

### Why `en.microsoft` analyzer?

Azure Search offers `standard` (basic Lucene) and language-specific analyzers. `en.microsoft` uses
Microsoft's NLP for English — better stemming, contraction handling, and stop word treatment than
standard Lucene. For a document corpus that's all English prose, this is an easy win at no cost.

---

## Why Direct HTTP (No Azure Go SDK)?

The Azure SDK for Go is available but not used for OpenAI or Search calls. Reasons:

1. **Transparency for learning:** Every HTTP call is visible. Readers can see the exact URL, headers,
   and JSON body being sent. No magic methods hiding what's happening over the wire.

2. **The Azure OpenAI Go SDK was in preview** when this was written. The REST API is stable.

3. **Fewer dependencies = easier to understand the go.mod.** Only `azblob` uses the SDK (Blob Storage
   — it's complex enough to justify the dependency).

4. **Debugging:** Direct HTTP calls can be reproduced verbatim with `curl`. SDK calls require
   understanding the SDK's own request building.

**Downside:** We re-implement retry logic, auth header injection, and error parsing that the SDK
would give us for free. See `internal/azure/openai.go` lines 50–55 (auth header) and 142–156 (retry).

If adding a new Azure service, use the SDK if the service's REST API is complex (Blob, EventHub)
or if the SDK is stable. Avoid it if you want learning transparency.

---

## DOCX Parsing Without External Libraries

`internal/ingest/docx.go` parses `.docx` files using only stdlib.

**Why:** The main Go DOCX libraries (`unioffice`, `go-docx`) are either GPL-licensed, heavy, or
have poor support for numbered lists and custom styles. The OOXML format (`.docx` is a ZIP of XML
files) is well-documented and straightforward to parse for the fields we need.

**How a `.docx` is structured:**
```
document.docx (ZIP archive)
├── word/
│   ├── document.xml     ← paragraph content + style references
│   ├── styles.xml       ← style definitions (Heading1 → "1", etc.)
│   └── numbering.xml    ← list numbering definitions
├── [Content_Types].xml
└── _rels/
```

**What we parse:**
- `word/document.xml` — paragraph by paragraph using a streaming XML token decoder
- Each `<w:p>` element = one paragraph
- `<w:pStyle w:val="...">` = style name
- `<w:t>` elements inside a paragraph = text runs (concatenated)
- `<w:numPr>` = numbered list indicator (ilvl = indent level)

**What we don't parse:**
- Tables (`<w:tbl>`) — table rows are skipped (cover page change history tables benefit from this)
- Hyperlinks
- Footnotes
- Embedded images

**Key limitation:** The style name in `document.xml` is a style ID (e.g., `"1"`), not the human
name (e.g., `"Heading 1"`). The code looks up the display name in `styles.xml` to normalize to
`"Heading1"`, `"Normal"`, etc. If a document uses custom style names, they'll fall through to
`"Normal"` handling.

**Code:** `internal/ingest/docx.go`

---

## Metadata Extraction from File Paths

Banner PDF metadata (module, version, year) is extracted entirely from the file path. No PDF
metadata parsing is done.

**Why:** Ellucian Banner release note PDFs embed metadata inconsistently. Parsing the filename and
folder structure is more reliable.

**Rules (implemented in `parseMetadata`):**

| Field | Source | Example |
|-------|--------|---------|
| `banner_module` | Folder name in path | `.../banner/general/...` → `"General"` |
| `banner_version` | Filename regex | `Banner_General_9.3.37.2_...pdf` → `"9.3.37.2"` |
| `year` | Folder name matching `\d{4}` | `.../2026/...` → `"2026"` |

**Supported module folder names:** `general`, `finance`, `student`, `hr`, `alumni`, `accounts`,
`position`, `advancement`. Anything else maps to `"Unknown"`.

**Version regex:** `(\d+\.\d+[\.\d]*)` — matches `9.3`, `9.3.37`, `9.3.37.2`, etc.

**Implication:** If you rename a folder or put a PDF in the wrong place, its metadata will be wrong.
The file path IS the metadata. Name things correctly.

**Code:** `internal/ingest/ingest.go:328–377`

---

## Deterministic Chunk IDs

Each indexed document chunk needs a stable ID for Azure Search's merge-or-upload semantics.

**Why not random UUIDs?** Re-ingesting the same document would create duplicate chunks (different
IDs = different documents in the index).

**Why not sequential integers?** Race conditions if parallel ingestion is ever added. Also fragile
across restarts.

**Solution:** `MD5(filename + "|" + pageNumber + "|" + chunkIndex)`

This means:
- Same file + same page + same chunk index = same ID → safe re-ingest (updates in place)
- Different file OR different page OR different chunk position = different ID (no collisions in practice)

**Limitation:** If you change `CHUNK_SIZE` or `CHUNK_OVERLAP`, the chunks themselves change, but
if a new chunk accidentally produces the same filename+page+index as an old chunk, it will silently
overwrite it. This is acceptable — just set `overwrite=true` when changing chunking parameters.

**Code:** `internal/ingest/ingest.go:379–383`

---

## Prompt Construction

The grounded prompt passed to GPT-4o-mini is structured to minimize hallucination.

**System message (paraphrased):**
> You are a Banner upgrade assistant. Answer only from the provided documentation excerpts.
> If the answer isn't in the context, say so. Cite your sources using [1], [2], etc.

**User message structure:**
```
Use the following Banner documentation excerpts to answer the question.

=== CONTEXT ===

[1] Banner_General_9.3.37.2_ReleaseNotes.pdf (page 3) | Module: General | Version: 9.3.37.2
<chunk text>
---
[2] Banner_General_9.3.37.2_ReleaseNotes.pdf (page 7) | Module: General | Version: 9.3.37.2
<chunk text>
---

=== QUESTION ===
What are the prerequisites for Banner General 9.3.37.2?

=== ANSWER ===
```

**Why `temperature: 0.1`?** Higher temperature introduces creativity and variation. For factual
Q&A against documentation, creativity is the enemy — we want the model to repeat what the context
says, not paraphrase or infer.

**Why `max_tokens: 800`?** Keeps costs predictable and forces concise answers. Increase to 1200–1600
for summarization tasks (the summarize endpoints use different prompts but same client).

**Why numbered citations `[1]`, `[2]`?** GPT-4o-mini reliably maps these to the labeled sources.
The `SourceChunk` array in the response is in the same order, so `[1]` in the answer text corresponds
to `sources[0]` in the JSON.

**Code:** `internal/rag/rag.go:151–183`

---

## Rate Limit Handling

Azure OpenAI enforces TPM (Tokens Per Minute) limits. The deployments in this project use 10K TPM.

**During ingestion:**
- 500ms sleep between every embedding call (preemptive throttling)
- `internal/ingest/ingest.go` near each `openai.EmbedText()` call

**During embedding API calls:**
- If `HTTP 429` is returned, wait 15 seconds and retry (up to 3 attempts)
- `internal/azure/openai.go:142–156`

**During chat completion:**
- If `HTTP 429` is returned, wait 5 seconds, retry once, then 10 seconds, retry once
- Exponential backoff pattern
- `internal/azure/openai.go:80–105`

**What happens if retries are exhausted?** The function returns an error, the handler returns
`HTTP 500` with the error message. No partial responses are returned.

**To increase throughput:** Raise TPM limits in Azure OpenAI portal. The code does not need changes.
To ingest large document collections faster, remove or shorten the 500ms sleep (risky without
sufficient TPM).

---

## Config: Fail Fast vs. Graceful

`config/config.go` uses `requireEnv()` for mandatory fields:

```go
func requireEnv(key string) string {
    v := os.Getenv(key)
    if v == "" {
        log.Fatalf("required environment variable %s is not set", key)
    }
    return v
}
```

**Why panic/fatal on startup instead of returning an error?**

If the Azure OpenAI endpoint isn't configured, 100% of requests will fail. A server that starts but
fails every request is harder to diagnose than a server that refuses to start with a clear error
message.

**Fail-fast philosophy:** Surface configuration errors at startup, not at runtime.

**Optional variables** use `os.Getenv()` with documented defaults. The caller (usually a handler)
checks if the value is empty before using it — e.g., Blob Storage calls check if connection string
is set before attempting to use it.

---

## gRPC: Why It Exists and Its Current State

**Why add gRPC at all?**
- Demonstrates proto-first API design as a contrast to the HTTP REST approach
- gRPC streaming would enable real-time token streaming from GPT (future capability)
- Protocol Buffers provide stronger typing than JSON
- Educational: shows how to maintain two API surfaces from the same internal packages

**Current state:**
- Proto files are complete and correct (`proto/omnivore/v1/*.proto`)
- `buf.yaml` and `buf.gen.yaml` are configured
- gRPC server infrastructure is in place (`internal/grpcserver/server.go`)
- Service handler files exist but implementations are commented out (they await `buf generate`)
- Reflection is enabled so `grpcurl list` works once handlers are active

**To fully activate gRPC:**
1. Run `buf generate` (generates `gen/go/omnivore/v1/*.pb.go` and `*_grpc.pb.go`)
2. Uncomment imports in `internal/grpcserver/server.go`
3. Uncomment `RegisterXxxServiceServer()` calls
4. Implement handler methods in `banner.go`, `sop.go`, `system.go` — delegate to same `rag` and
   `ingest` packages that HTTP handlers use
5. Run `go run cmd/grpc/main.go`

**Why the handlers are stubbed:** `buf generate` produces files in `gen/go/` which is gitignored.
Uncommenting the imports before the files exist would break compilation for anyone who hasn't run
`buf generate`. The stubs let the gRPC server compile and start with an empty service registry.

---

## Known Limitations and Gotchas

### 1. PDF Text Extraction Quality

`github.com/ledongthuc/pdf` extracts text directly from PDF character streams. It works well for
text-based PDFs but will produce garbage or empty output for:
- Scanned PDFs (image-based) — no OCR is performed
- PDFs with unusual encoding or embedded fonts that remap character codes

**Symptom:** Chunks with garbage characters or empty text in the index.
**Fix:** Pre-process problem PDFs with a proper OCR tool (e.g., Azure AI Document Intelligence).

### 2. No Authentication

There is no API key, JWT, or any form of auth on any endpoint. Anyone who can reach the server on
port 8000 can ask questions, trigger ingestion, or recreate the index.

**Fine for:** Local development, internal tools on a private network
**Not fine for:** Any internet-facing deployment

### 3. Overwrite=true Deletes Everything

`POST /banner/ingest` with `{"overwrite": true}` calls `search.CreateIndex()` which **deletes the
entire index** including all SOP documents. Both Banner and SOP share the same index.

**Mitigation:** Always use `overwrite: false` unless you intend a full re-index of all documents.

### 4. SOP Filename Convention is Enforced

Files in `data/docs/sop/` that don't match `SOP\d+ - .+\.docx` are silently skipped with a log
warning. This is by design (avoids ingesting accidentally dropped files) but can be surprising.

### 5. Chunk Size is Global

`CHUNK_SIZE` and `CHUNK_OVERLAP` in `.env` apply to Banner PDFs only. SOP chunking is always
section-based and ignores these settings.

### 6. Index Schema is Fixed at Creation Time

The Azure AI Search index schema (field names, vector dimensions) is defined once in
`internal/azure/search.go:CreateIndex()`. Changing the schema requires:
1. Calling `POST /index/create` with `overwrite=true` (recreates the index, losing all data)
2. Re-ingesting all documents

You cannot add or rename fields in an existing Azure Search index without recreating it.

### 7. The `gen/go/` Directory

The `gen/go/` directory (protobuf-generated Go code) is gitignored. If you clone this repo and
try to build the gRPC server without running `buf generate` first, it will fail to compile if the
grpcserver package imports anything from `gen/go/`. Currently the imports are commented out to
prevent this.

---

## Where Things Can Go Wrong

| Symptom | Likely Cause | Where to Look |
|---------|-------------|---------------|
| `500 Internal Server Error` on `/banner/ask` | Azure OpenAI rate limit or wrong endpoint | `internal/azure/openai.go`, check logs for HTTP status |
| Empty `sources` array in response | No documents indexed, or wrong `source_type` filter | Check `/index/stats`, check `source_type` field in index |
| `mode=web` or `mode=hybrid` returns error | `TAVILY_API_KEY` not set | `config/config.go`, `internal/azure/tavily.go` |
| `mode=auto` always uses local despite low scores | Tavily client is nil (key not configured) | Set `TAVILY_API_KEY` in `.env` |
| `/banner/student/ask` returns unrelated Banner release notes | Wrong `source_type` — student guide uses `"banner_user_guide"` | `internal/azure/search.go` — `SourceTypeBannerGuide` constant |
| Module-scoped ask (`/banner/finance/ask`) returns results for wrong module | Module name in URL doesn't match indexed `banner_module` metadata | Check folder structure under `data/docs/banner/` and `parseMetadata()` |
| Garbled or empty PDF text | PDF is scanned / image-based | `internal/ingest/ingest.go:extractPDFPages()` |
| SOP file skipped during ingest | Filename doesn't match `SOP\d+ - .+\.docx` | `internal/ingest/sop.go:parseSopFilename()` |
| `index not found` error | Index not created yet | `POST /index/create` |
| All SOP data missing after banner ingest | `overwrite: true` was used | Recreate index and re-ingest SOPs |
| `required environment variable X is not set` | `.env` file missing or incomplete | `config/config.go` |
| Swagger UI returns 404 | `docs/` not generated | Run `go generate ./internal/api/` |
| gRPC server fails to start | `gen/go/` missing | Run `buf generate` |

---

## How to Extend

### Add a New Document Type

1. Create `internal/ingest/newtype.go` — parser that returns `[]string` (text blocks)
2. Add a new chunker if the structure differs from flat text (follow `sop_chunker.go` as a model)
3. Add metadata fields to `ChunkDocument` in `internal/ingest/ingest.go`
4. Add corresponding fields to the Azure Search index schema in `internal/azure/search.go:CreateIndex()`
5. Add a new `source_type` value (e.g., `"policy"`)
6. Add a new handler in `internal/api/handlers.go` following the `SopIngest` / `BannerIngest` pattern
7. Wire the route in `internal/api/router.go`

### Add a New Summarization Topic

Topics are defined as a map in `internal/rag/summarize.go`:

```go
var topicConfigs = map[string]TopicConfig{
    "changes": { SearchQuery: "...", SystemPrompt: "..." },
    // add new entries here
}
```

Add an entry, add a handler that calls `SummarizeTopic(req, "newtopic")`, wire the route. No other
changes needed.

### Add Authentication

Add Gin middleware in `internal/api/router.go`:

```go
r.Use(authMiddleware(cfg.APIKey))
```

Implement `authMiddleware` to check `Authorization: Bearer <key>` headers. Store the expected key
in `config.go` as an optional env var.

### Add Streaming Responses

Replace `openai.ChatComplete()` with a streaming version:
1. Call the Azure OpenAI chat endpoint with `"stream": true` in the request body
2. Read `text/event-stream` response chunks (server-sent events)
3. Use Gin's `c.Stream()` to forward chunks to the HTTP client
4. Update the handler to use `c.Header("Content-Type", "text/event-stream")`

This is the highest-impact UX improvement for the ask endpoints.

---

## Testing Gaps

**What's tested:**
- `internal/ingest/docx_test.go` — DOCX paragraph extraction (unit)
- `internal/ingest/sop_chunker_test.go` — SOP section chunking (unit)
- `internal/ingest/sop_test.go` — SOP filename parsing (unit)

**What's not tested (and why it's hard):**
- Azure clients — require live Azure credentials; no mock layer exists
- RAG pipeline — depends on Azure OpenAI + Azure Search; would need dependency injection to mock
- HTTP handlers — could be unit tested with `httptest.NewRecorder()` but would need Azure mocks
- End-to-end ingestion — requires a real file system + Azure Search index

**If you want to add integration tests:**
- Use a separate `.env.test` pointing at a test Azure Search index
- Add a `TestMain()` that checks for test env vars and skips if not present
- Test ingestion → query round trips against real data

**If you want to add unit tests for Azure clients:**
- Extract an interface (e.g., `Embedder`, `Searcher`) from the concrete types
- Inject mocks in tests
- Currently the concrete types are used directly everywhere — refactoring needed

---

## Performance Notes

### Ingestion Throughput

Bottleneck: Azure OpenAI embedding API (10K TPM limit with 500ms sleep between calls).

At 10K TPM with `text-embedding-ada-002` (~500 tokens/chunk):
- ~20 embeddings per minute (TPM-constrained)
- ~500ms sleep per embedding means ~120 embeddings per minute (sleep-constrained)
- Sleep is the actual bottleneck — remove it if you have sufficient TPM headroom

For a 100-page PDF with 200 chunks: ~1.7 minutes.
For 50 such PDFs: ~85 minutes. Plan accordingly.

### Query Latency

Typical breakdown for `/banner/ask`:
- Embedding: ~200–400ms (Azure OpenAI round trip)
- Hybrid search: ~50–150ms (Azure Search)
- Chat completion: ~1000–3000ms (GPT-4o-mini, depends on answer length)
- **Total: ~1.5–4 seconds**

The embedding call is unavoidable. Chat completion dominates — reduce `max_tokens` to cut this.

### Memory Usage

Ingestion holds one batch of 100 `ChunkDocument` structs in memory at a time. Each struct has a
1536-float32 vector (6144 bytes) plus text. For a 100-chunk batch: ~1–2 MB peak. Not a concern.

The Go garbage collector handles cleanup between batches. No explicit memory management needed.
