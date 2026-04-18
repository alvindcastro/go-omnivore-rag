# I Built a Chatbot That Actually Reads Banner Release Notes So We Don't Have To

Every higher-ed IT team I've worked with has the same ritual before a Banner upgrade. Someone downloads the release notes PDF — it's a hundred-plus pages — opens it in Acrobat, and starts Ctrl+F-ing through dense tables looking for the one breaking change that affects their custom modifications. It works, but it takes forever. Multiply that by Finance, General, Student, and HR modules, across two or three versions in flight, and you've turned a Tuesday afternoon into a week of archaeology.

I decided to build something to fix that.

## The Problem

Ellucian Banner release notes are thorough — I'll give them that. They document every API change, every database delta, every compatibility note. The problem is that thoroughness comes in the form of 80–120 pages of formatted PDFs per module per release. Staff hunting for one specific answer — "does this version change the GL posting table?" — have no good option except grepping by eye. The institutional knowledge of "which pages to check first" lives in whoever has done the most upgrades.

That knowledge should live in a system.

## Picking a Stack

Before writing a line of code I had to decide what to actually build on. The AI tooling ecosystem is moving fast — fast enough that the answer from six months ago might be wrong today. I spent time exploring what Azure has in the space: Azure AI Search, Azure OpenAI, Azure AI Document Intelligence (for OCR), Azure Blob Storage, and a handful of orchestration options from Azure Functions to Durable Functions to Logic Apps. There's no shortage of services. The question was which ones were actually necessary versus which ones sounded useful in a sales deck.

My conclusions: Azure AI Search and Azure OpenAI do the real work. Everything else is infrastructure glue. Azure AI Document Intelligence is great for scanned PDFs — but Ellucian's release notes are text-based, so standard PDF extraction handles them fine. Logic Apps are for multi-step business processes with lots of SaaS connectors; that's not this. I kept the core small.

For the language I chose Go. It sounds like an odd choice when Python has the richer ML library ecosystem, but Go buys things I actually care about here. Explicit error handling means I see every place an Azure call can fail. A single binary from `go build` means deployment is `scp` + `restart`. No JVM warmup, no interpreter. And `encoding/xml` and `archive/zip` are in the standard library, which matters later when I need to parse Word documents without pulling in a third-party library with an iffy license. The cost is verbose JSON marshalling and no native async/await — concurrency requires goroutines and channels when I eventually need them.

I also made a deliberate choice to skip the Azure SDK for Go on the OpenAI and Search calls. The Azure Go SDK for OpenAI was still in preview when I started, and even for the stable SDKs, raw HTTP calls mean every request is reproducible with `curl`. When something breaks at 11pm before an upgrade window, I want to see the exact URL and JSON body, not debug an SDK abstraction. Only Azure Blob Storage uses the SDK because the Blob REST API is complex enough to justify the dependency.

## The Architecture

The codebase has a strict dependency direction and I've kept to it:

```
cmd → internal/api → internal/rag → internal/azure
                   → internal/ingest → internal/azure
config ← everything (leaf, imports nothing internal)
```

`internal/azure` is a thin HTTP client layer — nothing but direct calls to Azure services. `internal/rag` owns the query pipeline; `internal/ingest` owns the ingestion pipeline. `internal/api` wires them to HTTP handlers via Gin. `cmd` is the entry point: load config, wire dependencies, start server. No circular imports. If you add a dependency that goes the other direction, it won't compile — Go enforces it.

The config layer uses fail-fast semantics: if a required environment variable is missing, the process panics at startup with a clear error. A server that starts but fails every request is harder to diagnose than one that refuses to start. This matters when you're deploying to Azure Container Apps and reading logs at 6am.

## The Build: RAG Explained and Implemented

RAG — Retrieval Augmented Generation — is the architecture that makes this work. Instead of asking GPT-4 to memorize all of Banner's release notes (it can't, and it would hallucinate anyway), you embed the documents into a vector index, retrieve the most relevant chunks at query time, and hand those chunks to the model as grounded context. The model answers from the documents; it cites its sources. If the answer isn't there, it says so.

Document ingestion reads PDFs page by page, chunks them at 500 characters with a 25-character overlap, sanitizes the text, embeds each chunk with `text-embedding-ada-002` into a 1536-dimensional float32 vector, and uploads batches of 100 to Azure Search. Metadata — module, version, year — comes entirely from the file path. A file at `data/docs/banner/finance/2026/february/Banner_Finance_9.3.22_ReleaseNotes.pdf` gets tagged `module=Finance`, `version=9.3.22`, `year=2026` automatically. The file path is the schema; name things correctly and the metadata takes care of itself.

Chunk IDs are deterministic: `MD5(filename + "|" + page + "|" + chunkIndex)`. Re-ingest the same file and Azure Search merges in place rather than duplicating. Change chunk size and you get new IDs — exactly the behavior you want when re-indexing after a parameter change.

At query time, the pipeline embeds the question, runs a hybrid search with optional `module_filter`, `version_filter`, and `year_filter` OData predicates, and builds a numbered prompt — `[1] ... [2] ...` — that GPT-4o-mini answers from at temperature 0.1. Low temperature means the model repeats what the documents say, not what it imagines. Citations in the response text map directly to the `sources` array in the JSON.

### The Azure AI Search Index

The hybrid search is where Azure AI Search earns its place. Each query runs BM25 keyword match on the chunk text and document title simultaneously with HNSW vector similarity on the embedding — one round trip, one score. I spent time tuning the HNSW parameters: `m: 4` (bidirectional links per node, lower than the typical default of 16 because the index is small — thousands of chunks, not millions), `efConstruction: 400` (build-time search quality), `efSearch: 500` (query-time recall). For this corpus size these settings are fine; at millions of documents the lower `m` would start hurting recall noticeably.

The text analyzer is `en.microsoft` rather than standard Lucene. Microsoft's NLP handles English stemming, contractions, and stop words better. It's a free upgrade and the corpus is entirely English prose.

One index holds everything — Banner release notes, SOPs, user guides — separated by a `source_type` field. Two indexes would double the management overhead for schemas that are 99% identical. The filter achieves the same isolation with zero additional infrastructure.

### Banner User Guides

Beyond release notes, we also ingest the Banner user guide PDFs. Staff often know a process exists but blank on the exact screen, field name, or navigation path. Having user guide content in the same retrieval pipeline means a question like "where do I enter a budget transfer?" returns the actual form walkthrough, not just the release note that mentions the form changed. It turns the chatbot from a change-tracking tool into something closer to a knowledge base for day-to-day Banner use. The `source_type` filter keeps user guide queries cleanly separated from release note queries — same index, different retrieval path.

## Adding SOPs

We're the internal users of this tool, so we felt the gaps firsthand. Release notes tell you what changed; they don't tell you what to do about it. The natural next question — "how do I run the smoke test after an upgrade?", "what's the procedure for stopping Axiom?" — lives in SOPs, not release notes. Adding them was an obvious extension.

SOPs have structure that PDFs don't. The release note chunks are character-based because extracted PDF text has no reliable semantic boundaries. A Word document, by contrast, has headings — Heading1 through Heading4, numbered sections — and those heading boundaries are exactly where you want to split.

The SOP chunker parses `word/document.xml` with a streaming XML token decoder. No external library: `.docx` is a ZIP archive, `archive/zip` opens it, `encoding/xml` decodes the paragraphs token by token. Each `<w:p>` element is a paragraph; the style attribute tells you if it's a heading. There's a wrinkle: the style name in the document XML is a style ID (e.g., `"1"`), not the human-readable name (`"Heading 1"`). The code cross-references `styles.xml` to normalize. Two heading detection modes handle both explicitly styled paragraphs and numbered-paragraph conventions like `6.2 Stopping Axiom`.

Every SOP chunk carries a breadcrumb prefix: `[SOP 122 — Smoke Test and Sanity Test Post Banner Upgrade] > 6. Detailed Procedures > 6.2 Smoke Test Steps`. The breadcrumb gets embedded along with the text, so vector similarity carries section context. GPT-4o-mini knows which SOP a chunk came from and where in that SOP it lives, without having to infer it from metadata.

## The Chatbot Layer

A Go RAG backend with a curl interface is useful for engineers. It's not useful for functional analysts who want to type a question and get an answer. That gap led to a Botpress adapter — a thin Go service sitting between Botpress Cloud and the RAG backend.

Every message flows through three adapter endpoints. First, `/chat/sentiment` scores for frustration using a rule-based classifier — no ML dependency, no external call. Higher scores route to a human escalation message before the question even reaches retrieval. Second, `/chat/intent` classifies into one of six intents: `BannerRelease`, `BannerFinance`, `SopQuery`, `BannerAdmin`, `BannerUsage`, or `General`. Third, `/chat/ask` routes to the right backend endpoint based on that intent — `SopQuery` goes to `/sop/ask`, `BannerFinance` goes to `/banner/ask` with `module_filter=Finance`, `BannerUsage` goes to `/banner/general/ask` with `source_type=banner_user_guide`. Callers can override with an explicit `source` field if they know what they want.

The adapter returns a `confidence` score and an `escalate` flag. Confidence is `sources[0].score` — the raw Azure AI Search hybrid score. After a calibration run, valid answers on this corpus score 0.0325–0.0331. That sounds low, but that's the observed range; the index doesn't return normalized 0–1 values. The escalation gate is `retrieval_count == 0` (hard gate — no documents means nothing to answer from) or `confidence < 0.01` (soft guard for noise). I learned that the hard way after setting an initial threshold that was three orders of magnitude too high; every answer was being escalated and nobody trusted the bot.

## Deployment and Cost

The backend runs on Fly.io for dev and demo — cheap, simple, works out of the box for a stateless Go binary. The production path is Azure Container Apps: build the Docker image, push to Azure Container Registry, deploy as a Container App with `min-replicas 0` for scale-to-zero. The image is a two-stage Docker build: a `golang:1.24-bookworm` builder stage runs `go build` and produces the binary, then a `debian:bookworm-slim` runtime image carries only the binary and CA certificates. No Go toolchain in production.

Cost at low volume is real but modest. Azure OpenAI and Azure AI Search together run $1–5/month for a dev instance. Add Azure Container Apps on Flex Consumption (scale-to-zero), Container Registry Basic tier, and Azure Files for document storage, and you're looking at roughly $10–25/month fully deployed. The dominant cost lever is how often the container receives requests — idle time is free when you're at zero replicas.

The Azure OpenAI deployment runs at 10K TPM. During ingestion there's a 500ms sleep between every embedding call as preemptive rate limiting, with exponential backoff retry on HTTP 429. For a 100-page PDF with ~200 chunks, ingest takes about two minutes. Fifty such PDFs: 85 minutes. Plan a batch run accordingly, or bump the TPM limit in the Azure portal — the code doesn't need to change.

## What's Next

A few things I'd do differently now. Ingestion is synchronous and request-scoped; a large batch blocks for the duration. The right fix is Azure Service Bus with a Blob Storage trigger — drop a PDF in the right container and the ingest job fires automatically, with retry and dead-letter built in. Right now someone has to call `POST /banner/ingest` by hand.

I also want streaming responses from the chat endpoint. The typical query roundtrip is 1.5–4 seconds (200–400ms embedding, 50–150ms search, 1–3s chat completion). Streaming from Azure OpenAI's `text/event-stream` response and forwarding via Gin's `c.Stream()` would make that latency feel instant even if it's the same total time. That's the single highest-impact UX change left.

Longer-term: Durable Functions for parallel module summarization (fan out to four GPT calls simultaneously instead of serially), a confidence drift dashboard so I know when the corpus has grown enough to shift the calibration floor, and a zero-shot model fallback for the intent classifier when keyword matching is ambiguous. The wiki has the full roadmap.

The work continues on evenings and weekends, the way the best side projects always do.

---

The repo is at [github.com/alvin-dcastro/go-omnivore-rag](https://github.com/alvin-dcastro/go-omnivore-rag) — Go 1.24, Azure OpenAI, Azure AI Search, Botpress Cloud, Fly.io. Stack is MIT licensed; the release notes themselves are Ellucian's and stay out of the repo.
