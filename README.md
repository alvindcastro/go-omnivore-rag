# go-omnivore-rag

> **Educational Project** — This project demonstrates how to build an Internal Knowledge Assistant (RAG System) using Go and Microsoft Azure AI services. It is intended as a learning exercise for developers interested in RAG architecture, Azure OpenAI, and Go REST APIs.
>
> You will need your own Azure subscription and Ellucian Banner license to use with real documents.

---

## What This Project Does

An AI-powered assistant that helps IT staff and functional analysts answer questions about Ellucian Banner ERP upgrades. Instead of manually reading through lengthy release notes PDFs, you can:

- **Ask natural language questions** — "What are the prerequisites for Banner General 9.3.37.2?"
- **Get structured summaries** — Breaking changes, action items, compatibility requirements
- **Filter by module, version, and year** — Scope answers to exactly what you need
- **Ingest any Banner PDF** — Drop it in the right folder and index it instantly

---

## Architecture

```
Banner PDF Release Notes
        │
        ▼
  POST /ingest                    ← Parse PDF → chunk → embed → index
        │
        ▼
  Azure AI Search Index           ← Vector + keyword hybrid search
        │
        ▼
  POST /ask                       ← Question → embed → search → GPT-4o-mini → answer
  POST /summarize/full            ← Retrieve chunks → GPT-4o-mini → structured summary
```

**Stack:**
- **Go 1.22+** with Gin web framework
- **Azure OpenAI** — GPT-4o-mini (chat) + text-embedding-ada-002 (embeddings)
- **Azure AI Search** — Hybrid vector + BM25 keyword search
- **Azure Blob Storage** — Optional PDF source storage
- **Bruno** — API testing (recommended over Postman)

---

## Project Structure

```
go-omnivore-rag/
├── cmd/
│   └── main.go                        ← Entry point
├── config/
│   └── config.go                      ← Loads .env settings
├── internal/
│   ├── azure/
│   │   ├── openai.go                  ← Azure OpenAI REST client (embed + chat)
│   │   ├── search.go                  ← Azure AI Search REST client (index + search)
│   │   └── blob.go                    ← Azure Blob Storage SDK client
│   ├── ingest/
│   │   ├── ingest.go                  ← PDF/TXT/MD parse → chunk → embed → index pipeline
│   │   ├── docx.go                    ← DOCX paragraph extractor (no external deps)
│   │   ├── sop.go                     ← SOP filename/metadata parser
│   │   └── sop_chunker.go             ← Section-aware SOP chunker with breadcrumbs
│   ├── rag/
│   │   ├── rag.go                     ← RAG pipeline (retrieve + generate)
│   │   └── summarize.go               ← Summarization pipeline (4 focused topics)
│   └── api/
│       ├── handlers.go                ← HTTP handlers for all endpoints
│       └── router.go                  ← Gin route wiring
├── data/
│   └── docs/                          ← Drop Banner PDFs here
│       └── <module>/
│           └── <year>/
│               └── <month>/
│                   └── Banner_<Module>_<Version>_ReleaseNotes.pdf
├── .env.example                       ← Copy to .env and fill in Azure credentials
├── .gitignore
└── go.mod
```

---

## Azure Services Required

| Service | Tier | Purpose |
|---|---|---|
| Azure OpenAI | Standard S0 | GPT-4o-mini (chat) + text-embedding-ada-002 (embeddings) |
| Azure AI Search | Free | Vector + hybrid search index |
| Azure Blob Storage | Standard LRS | Optional — store Banner PDFs in the cloud |

**Estimated cost for MVP/learning:** ~$1–5/month

---

## Setup

### 1. Prerequisites
- Go 1.22+ — [go.dev/dl](https://go.dev/dl)
- Azure subscription — [portal.azure.com](https://portal.azure.com)
- GoLand or VS Code with Go extension

### 2. Clone and install
```bash
git clone https://github.com/<your-username>/go-omnivore-rag.git
cd go-omnivore-rag
go mod tidy
```

### 3. Create Azure resources

**Resource Group**
```
Name: rg-omnivore-rag
Region: Canada Central
```

**Azure OpenAI** (Canada East)
```
Name: omnivore-rag-openai
Tier: Standard S0
Deployments:
  - gpt-4o-mini (version 2024-07-18, 10K TPM)
  - text-embedding-ada-002 (version 2, 10K TPM)
```

**Azure AI Search** (Canada Central)
```
Name: omnivore-rag-search
Tier: Free
```

**Azure Blob Storage** (Canada Central)
```
Name: omnivoreragstorage
Redundancy: LRS
Container: banner-release-notes (Private)
```

### 4. Configure environment
```bash
cp .env.example .env
# Fill in your Azure credentials
```

### 5. Create the search index
```
POST http://localhost:8000/index/create
```

### 6. Add Banner documents

Create the folder structure and drop your PDFs in:
```
data/docs/banner/general/2026/february/Banner_General_9.3.37.2_ReleaseNotes.pdf
data/docs/banner/finance/2026/february/Banner_Finance_9.3.22_ReleaseNotes.pdf
data/docs/sop/your-procedure.pdf
```

**Naming convention** (for auto-detection of module and version):
```
Banner_<Module>_<Version>_ReleaseNotes.pdf
```

### 7. Run the server
```bash
go run cmd/main.go
```

### 8. Ingest documents
```bash
POST http://localhost:8000/ingest
{
  "docs_path": "data/docs",
  "overwrite": false,
  "pages_per_batch": 5,
  "start_page": 0,
  "end_page": 0
}
```

---

## API Endpoints

### System

| Method | Endpoint | Description |
|---|---|---|
| GET | `/health` | Check Azure connectivity |
| GET | `/index/stats` | Document count in search index |
| POST | `/index/create` | Create or recreate the search index |
| GET | `/debug/chunks` | List all indexed chunks (top 50) |

### RAG

| Method | Endpoint | Description |
|---|---|---|
| POST | `/ask` | Ask a natural language question |

**Ask request:**
```json
{
  "question": "What are the prerequisites for Banner General 9.3.37.2?",
  "top_k": 5,
  "module_filter": "General",
  "version_filter": "9.3.37.2",
  "year_filter": "2026"
}
```

All filter fields are optional — omit any you don't need.

### Ingestion

| Method | Endpoint | Description |
|---|---|---|
| POST | `/ingest` | Parse, chunk, embed, and index PDFs |

**Ingest request:**
```json
{
  "docs_path": "data/docs",
  "overwrite": false,
  "pages_per_batch": 5,
  "start_page": 0,
  "end_page": 0
}
```

| Field | Description |
|---|---|
| `pages_per_batch` | Pages to process per batch (default: 10) |
| `start_page` | Start from this page (0 = beginning) |
| `end_page` | Stop at this page (0 = end of document) |
| `overwrite` | Delete and recreate index before ingesting |

### Summarizer

| Method | Endpoint | Description |
|---|---|---|
| POST | `/summarize/changes` | What changed / new features |
| POST | `/summarize/breaking` | Breaking changes and deprecations |
| POST | `/summarize/actions` | Action items IT staff must perform |
| POST | `/summarize/compatibility` | Version and compatibility requirements |
| POST | `/summarize/full` | All four topics in one response |

**Summarize request (same for all endpoints):**
```json
{
  "filename": "Banner_General_Release_Notes_9.3.37.2_8.26.2_February_2026.pdf",
  "banner_module": "General",
  "banner_version": "9.3.37.2",
  "year_filter": "2026",
  "top_k": 20
}
```

**Full summary response example:**
```json
{
  "filename": "Banner_General_Release_Notes_9.3.37.2_8.26.2_February_2026.pdf",
  "banner_module": "General",
  "banner_version": "9.3.37.2",
  "what_changed": "- Enhanced GLRPACC page to support bulk assignment...\n- New BPAPI for population-selection-base-description...",
  "breaking_changes": "- Banner 8 Self-Service end of support as of January 1, 2026.",
  "action_items": "1. Execute gguroptmi_082602.sql DML script to add Bulk Edit Access option...",
  "compatibility": "- Release 9.3.37.2 and 8.26.2 are compatible with each other...",
  "source_pages": [9, 10, 11],
  "chunks_analyzed": 9
}
```

### Blob Storage

| Method | Endpoint | Description |
|---|---|---|
| GET | `/blob/list` | List PDFs in Azure Blob container |
| POST | `/blob/sync` | Download from Blob and ingest |

---

## Document Folder Structure

Documents are organized under `data/docs/` by input type. The ingestion pipeline automatically detects module and version from the folder path and filename:

```
data/docs/
├── banner/                          # Ellucian Banner release notes
│   ├── general/
│   │   └── 2026/
│   │       └── february/
│   │           └── Banner_General_9.3.37.2_ReleaseNotes.pdf
│   ├── finance/
│   │   └── 2026/
│   │       └── february/
│   │           └── Banner_Finance_9.3.22_ReleaseNotes.pdf
│   ├── student/
│   │   └── 2026/
│   │       └── february/
│   │           └── Banner_Student_9.39_ReleaseNotes.pdf
│   └── hr/
│       └── 2026/
│           └── february/
│               └── Banner_HR_9.28_ReleaseNotes.pdf
└── sop/                             # Standard Operating Procedures
    └── <your-sop-documents>
```

**Supported file types:** `.pdf`, `.txt`, `.md`, `.docx`

> **Note:** Legacy `.doc` files are not supported. Save them as `.docx` before ingesting.

---

## SOP Document Ingestion

Files under `data/docs/sop/` are processed through a dedicated section-aware pipeline instead of the standard page-based chunker.

**Filename convention** (required for auto-detection):
```
SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx
SOP154 - Procedure - Start, Stop Axiom.docx
```

The SOP number and title are extracted from the filename. Files that don't match the `SOP<N> - <Title>` pattern are skipped with a warning.

**How SOP chunking works:**

SOPs are split at every section heading. Each chunk is prefixed with a breadcrumb so the model always knows which SOP and section it came from, even when retrieved in isolation:

```
[SOP 154 — Procedure - Start, Stop Axiom] > 6. Detailed Procedures > 6.2 Stopping Axiom

<body text for this section>
```

Two heading styles are handled automatically:
- **Styled** — `Heading1` / `Heading2` / `Heading3` / `Heading4` Word paragraph styles (e.g. SOP122)
- **Plain** — `Normal` paragraphs with numbered prefixes like `6.2 Stopping Axiom` (e.g. SOP154)

Cover-page furniture (company info, change history tables, table of contents) is stripped before chunking.

SOP chunks are indexed with `source_type: "sop"` alongside the SOP number and document title, making them filterable independently from Banner release notes.

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
AZURE_SEARCH_INDEX_NAME=banner-upgrade-knowledge

# Azure Blob Storage
AZURE_STORAGE_CONNECTION_STRING=DefaultEndpointsProtocol=https;AccountName=...
AZURE_STORAGE_CONTAINER_NAME=banner-release-notes
AZURE_STORAGE_BLOB_PREFIX=

# RAG Tuning
CHUNK_SIZE=500
CHUNK_OVERLAP=25
TOP_K_DEFAULT=5

# API
API_PORT=8000
LOG_LEVEL=info
```

---

## Cost Control Tips

- Use **Free tier** for Azure AI Search during development
- Use **gpt-4o-mini** instead of gpt-4o — ~95% cheaper
- Set **10K TPM** limit on both OpenAI deployments
- Set a **budget alert** in Azure Cost Management ($20/month recommended)
- Only re-ingest when you have new documents — avoid unnecessary embedding calls
- Use `start_page` / `end_page` to ingest specific page ranges during testing

---

## Known Implementation Notes

- Azure OpenAI and Azure AI Search use **direct REST calls** (no official Go SDK) — intentional for transparency and learning
- Azure Blob Storage uses the **official Go SDK**
- DOCX parsing uses only `archive/zip` and `encoding/xml` — no external Word library required
- Banner PDFs use character-based chunking with sentence boundary detection
- SOP `.docx` files use section-aware chunking — each heading opens a new chunk with a breadcrumb prefix
- Hybrid search combines **vector similarity** (semantic) + **BM25 keyword** search
- Banner module and version are **auto-detected** from folder name and filename
- Year is **auto-detected** from the folder path (e.g. `2026/`)

---

## Bruno API Collection

Recommended tool for testing: [usebruno.com](https://www.usebruno.com)

Collection structure:
```
Omnivore RAG API/
├── System/
│   ├── Health Check
│   ├── Index Stats
│   └── Create Index
├── RAG/
│   └── Ask Question
├── Ingestion/
│   └── Ingest Documents
├── Blob Storage/
│   ├── Blob List
│   └── Blob Sync
├── Summarizer/
│   ├── What Changed
│   ├── Breaking Changes
│   ├── Action Items
│   ├── Compatibility
│   └── Full Summary
└── Debug/
    └── List Chunks
```

---

## License

MIT — free to use for learning and educational purposes.

---

## Disclaimer

This project is not affiliated with or endorsed by Ellucian. Banner is a trademark of Ellucian Company L.P. Real Banner release notes are licensed documents — do not share or redistribute them publicly.