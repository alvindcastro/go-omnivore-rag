# go-omnivore-rag

> **Educational Project** — Demonstrates how to build an Internal Knowledge Assistant (RAG System) using Go and Microsoft Azure AI services. Intended as a learning exercise for RAG architecture, Azure OpenAI, Go REST APIs, and gRPC.
>
> You will need your own Azure subscription to use with real documents.

---

## What This Project Does

An AI-powered assistant that helps IT staff and functional analysts answer questions about Ellucian Banner ERP upgrades and internal Standard Operating Procedures (SOPs). Instead of manually reading through lengthy PDFs and Word documents, you can:

- **Ask natural language questions** — "What are the prerequisites for Banner General 9.3.37.2?" or "How do I start the Axiom server?"
- **Get structured summaries** — Breaking changes, action items, compatibility requirements
- **Filter by module, version, and year** — Scope Banner answers to exactly what you need
- **Browse all indexed SOPs** — List every SOP with its title and chunk count
- **Ingest any supported document** — Drop it in the right folder and index it instantly

---

## Architecture

```
Banner PDFs / SOP .docx files
        │
        ▼
  POST /banner/ingest          ← PDF → pages → chunks → embed → index
  POST /sop/ingest             ← DOCX → paragraphs → sections → embed → index
        │
        ▼
  Azure AI Search Index        ← Vector + BM25 hybrid search (source_type filter)
        │
        ▼
  POST /banner/ask             ← Question → embed → search[banner] → GPT-4o-mini → answer
  POST /sop/ask                ← Question → embed → search[sop]    → GPT-4o-mini → answer
  POST /banner/summarize/full  ← Retrieve chunks → GPT-4o-mini → structured summary
```

**Stack:**
- **Go 1.24+** with Gin web framework (HTTP) and gRPC
- **Azure OpenAI** — GPT-4o-mini (chat) + text-embedding-ada-002 (embeddings)
- **Azure AI Search** — Hybrid vector + BM25 keyword search, single index with `source_type` filtering
- **Azure Blob Storage** — Optional Banner PDF source storage
- **Protocol Buffers + buf** — gRPC service definitions and code generation

---

## Prerequisites

- **Go 1.24+** — [go.dev/dl](https://go.dev/dl)
- **buf** (for gRPC code generation) — [buf.build/docs/installation](https://buf.build/docs/installation)
- Azure subscription — [portal.azure.com](https://portal.azure.com)

---

## Azure Services Required

| Service | Tier | Purpose |
|---|---|---|
| Azure OpenAI | Standard S0 | GPT-4o-mini (chat) + text-embedding-ada-002 (embeddings) |
| Azure AI Search | Free | Vector + hybrid search index |
| Azure Blob Storage | Standard LRS | Optional — store Banner PDFs in the cloud |

**Estimated cost for MVP/learning:** ~$1–5/month

**Azure OpenAI** (Canada East)
```
Name: omnivore-rag-openai
Tier: Standard S0
Deployments:
  - gpt-4o-mini           (version 2024-07-18, 10K TPM)
  - text-embedding-ada-002 (version 2,           10K TPM)
```

**Azure AI Search** (Canada Central)
```
Name: omnivore-rag-search
Tier: Free
```

**Azure Blob Storage** (Canada Central, optional)
```
Name: omnivoreragstorage
Redundancy: LRS
Container: banner-release-notes (Private)
```

---

## Setup

### 1. Clone and install

```bash
git clone https://github.com/<your-username>/go-omnivore-rag.git
cd go-omnivore-rag
go mod tidy
```

### 2. Configure environment

```bash
cp .env.example .env
# Fill in your Azure credentials — see .env Configuration section below
```

### 3. Run the HTTP server

```bash
go run cmd/main.go
# Listening on :8000
```

### 4. Create the search index

```
POST http://localhost:8000/index/create
```

### 5. Add documents and ingest

Drop your files into the right folder:
```
data/docs/banner/general/2026/february/Banner_General_9.3.37.2_ReleaseNotes.pdf
data/docs/sop/SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx
data/docs/sop/SOP154 - Procedure - Start, Stop Axiom.docx
```

Then ingest:
```
POST http://localhost:8000/banner/ingest
POST http://localhost:8000/sop/ingest
```

---

## HTTP API

### System

| Method | Endpoint | Description |
|---|---|---|
| GET | `/health` | Service health and model info |
| GET | `/index/stats` | Document count in search index |
| POST | `/index/create` | Create or recreate the search index |
| GET | `/debug/chunks` | List indexed chunks (top 50) |

### Banner

| Method | Endpoint | Description |
|---|---|---|
| POST | `/banner/ask` | Ask a question about Banner release notes |
| POST | `/banner/ingest` | Ingest PDFs from `data/docs/banner` |
| GET | `/banner/blob/list` | List PDFs in Azure Blob container |
| POST | `/banner/blob/sync` | Download from Blob and ingest |
| POST | `/banner/summarize/changes` | What changed / new features |
| POST | `/banner/summarize/breaking` | Breaking changes and deprecations |
| POST | `/banner/summarize/actions` | Action items IT staff must perform |
| POST | `/banner/summarize/compatibility` | Version and compatibility requirements |
| POST | `/banner/summarize/full` | All four topics in one response |

**Ask request:**
```json
{
  "question": "What are the prerequisites for Banner General 9.3.37.2?",
  "top_k": 5,
  "version_filter": "9.3.37.2",
  "module_filter": "General",
  "year_filter": "2026"
}
```

All filter fields are optional.

**Ingest request:**
```json
{
  "docs_path": "data/docs/banner",
  "overwrite": false,
  "pages_per_batch": 10
}
```

**Summarize request:**
```json
{
  "version": "9.3.37.2",
  "module": "General",
  "top_k": 20
}
```

### SOP

| Method | Endpoint | Description |
|---|---|---|
| GET | `/sop` | List all indexed SOPs |
| POST | `/sop/ask` | Ask a question about SOPs |
| POST | `/sop/ingest` | Ingest `.docx` files from `data/docs/sop` |

**Ask request:**
```json
{
  "question": "How do I stop the Axiom server?",
  "top_k": 5
}
```

**Ingest request:**
```json
{
  "overwrite": false
}
```

---

## Swagger UI

Interactive API docs are auto-generated from handler comments using [swaggo/swag](https://github.com/swaggo/swag).

**Browse the UI** while the server is running:
```
http://localhost:8000/docs/index.html
```

**Install the CLI** (once, to regenerate after handler changes):
```bash
go install github.com/swaggo/swag/cmd/swag@latest
```

**Regenerate docs:**
```bash
go generate ./internal/api/
```

The `docs/` directory is gitignored — always regenerate after pulling changes to `internal/api/handlers.go`.

---

## Bruno API Collection

Recommended tool for manual testing: [usebruno.com](https://www.usebruno.com)

Open the `apis/Omnivore RAG API/` folder in Bruno. Set the `base_url` environment variable to `http://localhost:8000`.

```
Omnivore RAG API/
├── System/
│   ├── Health Check         GET  /health
│   ├── Index Stats          GET  /index/stats
│   └── Create Index         POST /index/create
├── Banner/
│   ├── Ask                  POST /banner/ask
│   ├── Ingest               POST /banner/ingest
│   ├── Blob List            GET  /banner/blob/list
│   ├── Blob Sync            POST /banner/blob/sync
│   ├── Summarize What Changed        POST /banner/summarize/changes
│   ├── Summarize Breaking Changes    POST /banner/summarize/breaking
│   ├── Summarize Action Items        POST /banner/summarize/actions
│   ├── Summarize Compatibility       POST /banner/summarize/compatibility
│   └── Summarize Full                POST /banner/summarize/full
├── SOP/
│   ├── List SOPs            GET  /sop
│   ├── Ask                  POST /sop/ask
│   └── Ingest               POST /sop/ingest
└── Debug/
    └── List Chunks          GET  /debug/chunks
```

---

## Working with Documents

### Folder Structure

```
data/docs/
├── banner/                          # Ellucian Banner release notes
│   ├── general/
│   │   └── 2026/february/
│   │       └── Banner_General_9.3.37.2_ReleaseNotes.pdf
│   ├── finance/
│   │   └── 2026/february/
│   │       └── Banner_Finance_9.3.22_ReleaseNotes.pdf
│   └── student/
│       └── 2026/february/
│           └── Banner_Student_9.39_ReleaseNotes.pdf
└── sop/                             # Standard Operating Procedures
    ├── SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx
    └── SOP154 - Procedure - Start, Stop Axiom.docx
```

**Supported file types:** `.pdf`, `.txt`, `.md`, `.docx`

> Legacy `.doc` files are not supported. Save them as `.docx` before ingesting.

### SOP Ingestion Pipeline

Files under `data/docs/sop/` go through a dedicated section-aware pipeline instead of the standard page-based chunker.

**Filename convention** (required):
```
SOP<number> - <Title>.docx
SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx
SOP154 - Procedure - Start, Stop Axiom.docx
```

Files that don't match the `SOP<N> - <Title>` pattern are skipped with a warning.

**How chunking works:**

Each section heading opens a new chunk. Every chunk is prefixed with a breadcrumb so the model always knows which SOP and section it came from:

```
[SOP 154 — Procedure - Start, Stop Axiom] > 6. Detailed Procedures > 6.2 Stopping Axiom

Notify the team and/or clients.
Stop Windows Services in the following order: ...
```

Two heading styles are handled automatically:
- **Styled** — `Heading1` / `Heading2` / `Heading3` / `Heading4` Word paragraph styles
- **Plain** — `Normal` paragraphs with numbered prefixes like `6.2 Stopping Axiom`

Cover-page content (company info, change history tables, table of contents) is stripped before chunking. Chunks are indexed with `source_type: "sop"` and are fully filterable from Banner content.

---

## gRPC

The project exposes the same capabilities over gRPC alongside the HTTP API. Both servers run independently and share the same `config`, `azure`, `ingest`, and `rag` packages.

```
HTTP  → :8000  (Gin)       — REST / JSON
gRPC  → :9000  (net/grpc)  — Protocol Buffers
```

### Services

| Service | Methods |
|---|---|
| `SystemService` | `Health`, `IndexStats`, `CreateIndex` |
| `BannerService` | `Ask`, `Ingest`, `BlobList`, `BlobSync`, `SummarizeChanges`, `SummarizeBreaking`, `SummarizeActions`, `SummarizeCompatibility`, `SummarizeFull` |
| `SOPService` | `Ask`, `Ingest`, `List` |

Proto definitions live in `proto/omnivore/v1/`.

### Install buf

```bash
# macOS
brew install bufbuild/buf/buf

# Windows (PowerShell — via Scoop)
scoop install buf

# Or download directly from:
# https://github.com/bufbuild/buf/releases
```

Verify:
```bash
buf --version
```

### Generate Go code from proto files

```bash
buf generate
```

This reads `buf.gen.yaml` and writes generated files to `gen/go/omnivore/v1/`:
- `*.pb.go` — message types (from `protocolbuffers/go` plugin)
- `*_grpc.pb.go` — service interfaces and client stubs (from `grpc/go` plugin)

The `gen/go/` directory is gitignored — always regenerate after pulling changes to `.proto` files.

### Activate the gRPC handlers

After generating, uncomment the imports and implementations in:

```
internal/grpcserver/server.go   ← service registration
internal/grpcserver/system.go   ← SystemService
internal/grpcserver/banner.go   ← BannerService
internal/grpcserver/sop.go      ← SOPService
```

### Run the gRPC server

```bash
go run cmd/grpc/main.go
# gRPC server listening on :9000
```

Run both servers together (two terminals):
```bash
# Terminal 1
go run cmd/main.go

# Terminal 2
go run cmd/grpc/main.go
```

### Test with grpcurl

```bash
# Install grpcurl
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest

# List available services (reflection is enabled)
grpcurl -plaintext localhost:9000 list

# Health check
grpcurl -plaintext localhost:9000 omnivore.v1.SystemService/Health

# Ask a SOP question
grpcurl -plaintext -d '{"question":"How do I start Axiom?","top_k":3}' \
  localhost:9000 omnivore.v1.SOPService/Ask

# List SOPs
grpcurl -plaintext localhost:9000 omnivore.v1.SOPService/List
```

### Lint and breaking-change detection

```bash
# Lint proto files
buf lint

# Check for breaking changes against git HEAD
buf breaking --against '.git#branch=main'
```

---

## Project Structure

```
go-omnivore-rag/
├── cmd/
│   ├── main.go                        ← HTTP server entry point  (port 8000)
│   └── grpc/
│       └── main.go                    ← gRPC server entry point  (port 9000)
├── proto/
│   └── omnivore/v1/
│       ├── common.proto               ← Shared messages (AskResponse, SourceChunk, …)
│       ├── system.proto               ← SystemService (Health, IndexStats, CreateIndex)
│       ├── banner.proto               ← BannerService (Ask, Ingest, BlobSync, Summarize)
│       └── sop.proto                  ← SOPService (Ask, Ingest, List)
├── gen/
│   └── go/                            ← Generated protobuf code (run `buf generate`)
├── buf.yaml                           ← buf module config (lint + breaking detection)
├── buf.gen.yaml                       ← buf code generation config
├── config/
│   └── config.go                      ← Loads all settings from .env
├── internal/
│   ├── azure/
│   │   ├── openai.go                  ← Azure OpenAI REST client (embed + chat)
│   │   ├── search.go                  ← Azure AI Search REST client (index + hybrid search)
│   │   └── blob.go                    ← Azure Blob Storage SDK client
│   ├── ingest/
│   │   ├── ingest.go                  ← Ingestion pipeline (walk → extract → chunk → embed → index)
│   │   ├── docx.go                    ← DOCX paragraph extractor (no external deps)
│   │   ├── sop.go                     ← SOP filename/metadata parser
│   │   └── sop_chunker.go             ← Section-aware SOP chunker with breadcrumbs
│   ├── rag/
│   │   ├── rag.go                     ← RAG pipeline (retrieve + generate)
│   │   └── summarize.go               ← Summarization pipeline (4 focused topics)
│   ├── api/
│   │   ├── handlers.go                ← HTTP handlers (Gin)
│   │   └── router.go                  ← Gin route wiring
│   └── grpcserver/
│       ├── server.go                  ← gRPC server setup and service registration
│       ├── system.go                  ← SystemService handler implementation
│       ├── banner.go                  ← BannerService handler implementation
│       └── sop.go                     ← SOPService handler implementation
├── apis/
│   └── Omnivore RAG API/              ← Bruno HTTP collection
├── data/
│   └── docs/
│       ├── banner/                    ← Banner release note PDFs
│       └── sop/                       ← SOP .docx files
├── .env.example
└── go.mod
```

---

## .env Configuration

```env
# Azure OpenAI
AZURE_OPENAI_ENDPOINT=https://<your-resource>.openai.azure.com/
AZURE_OPENAI_API_KEY=<your-api-key>
AZURE_OPENAI_API_VERSION=2024-12-01-preview
AZURE_OPENAI_CHAT_DEPLOYMENT=gpt-4o-mini
AZURE_OPENAI_EMBEDDING_DEPLOYMENT=text-embedding-ada-002

# Azure AI Search
AZURE_SEARCH_ENDPOINT=https://<your-search>.search.windows.net
AZURE_SEARCH_API_KEY=<your-search-admin-key>
AZURE_SEARCH_INDEX_NAME=omnivore-knowledge

# Azure Blob Storage (optional)
AZURE_STORAGE_CONNECTION_STRING=DefaultEndpointsProtocol=https;AccountName=...
AZURE_STORAGE_CONTAINER_NAME=banner-release-notes
AZURE_STORAGE_BLOB_PREFIX=

# RAG Tuning
CHUNK_SIZE=500
CHUNK_OVERLAP=25
TOP_K_DEFAULT=5

# API
API_PORT=8000
GRPC_PORT=9000
LOG_LEVEL=info
```

---

## Cost Control Tips

- Use **Free tier** for Azure AI Search during development
- Use **gpt-4o-mini** instead of gpt-4o — ~95% cheaper
- Set **10K TPM** limits on both OpenAI deployments
- Set a **budget alert** in Azure Cost Management ($20/month recommended)
- Only re-ingest when you have new documents — avoid unnecessary embedding calls
- Use `start_page` / `end_page` on Banner ingest to test specific page ranges

---

## Implementation Notes

- Azure OpenAI and Azure AI Search use **direct REST calls** (no official Go SDK) — intentional for transparency
- Azure Blob Storage uses the **official Go SDK**
- DOCX parsing uses only `archive/zip` and `encoding/xml` — no external Word library
- Banner PDFs use character-based chunking with sentence boundary detection
- SOP `.docx` files use section-aware chunking with breadcrumb prefixes
- Hybrid search combines **vector similarity** (semantic) + **BM25 keyword** search
- A single Azure AI Search index stores both Banner and SOP documents, separated by `source_type`
- gRPC server exposes the same functionality as the HTTP API — both share all internal packages
- gRPC reflection is enabled so `grpcurl` and gRPC UI can discover services without `.proto` files

---

## License

MIT — free to use for learning and educational purposes.

---

## Disclaimer

This project is not affiliated with or endorsed by Ellucian. Banner is a trademark of Ellucian Company L.P. Real Banner release notes are licensed documents — do not share or redistribute them publicly.
