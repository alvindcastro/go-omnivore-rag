# go-omnivore-rag

> **Educational project** — RAG system + Botpress chatbot over Ellucian Banner ERP documentation, built with Go and Azure AI services.

---

## What this is

Two things in one repo:

1. **go-omnivore-rag** — Go REST + gRPC backend that ingests Banner release-note PDFs, SOPs, and user-guide PDFs into Azure AI Search and answers questions over them using GPT-4o-mini.
2. **Ask Banner adapter** (`internal/adapter`, `internal/intent`, `internal/sentiment`, `api/`) — thin Botpress adapter that exposes `/chat/ask`, `/chat/intent`, `/chat/sentiment` and routes to the right backend endpoint based on intent.

---

## Architecture

```
[Botpress Cloud Widget]
        ↓
[Botpress Flow — Execute Code nodes]
        ↓  axios → ask-banner.fly.dev
[Ask Banner Adapter]
    ├── POST /chat/sentiment  → rule-based frustration pre-filter
    ├── POST /chat/intent     → keyword classifier (6 intents)
    └── POST /chat/ask        → routes to backend by intent/source
                ↓
[go-omnivore-rag — :8000]
    ├── /banner/ask           module_filter=General|Finance  (release notes)
    ├── /banner/general/ask   source_type=banner_user_guide
    ├── /banner/student/ask   source_type=banner_user_guide
    ├── /banner/finance/ask   source_type=banner_user_guide
    ├── /sop/ask
    └── /banner/summarize/full
                ↓
[Azure OpenAI GPT-4o-mini + Azure AI Search hybrid]
```

**Stack:** Go 1.24 · Gin · Azure OpenAI · Azure AI Search · Azure Blob Storage · Protocol Buffers + buf

---

## Prerequisites

- Go 1.24+
- Azure subscription (OpenAI + AI Search; Blob Storage optional)
- buf — for gRPC code generation only

---

## Quick start

```bash
git clone https://github.com/<your-username>/go-omnivore-rag.git
cd go-omnivore-rag
go mod tidy
cp .env.example .env          # fill in Azure credentials
go generate ./internal/api/   # generate Swagger docs
go run cmd/main.go            # HTTP on :8000
```

First run — create the index and ingest:
```
POST /index/create
POST /banner/ingest
POST /sop/ingest
```

> Full setup walkthrough: [wiki/RUNBOOK.md](wiki/RUNBOOK.md)

---

## Azure services

| Service | Tier | Purpose |
|---|---|---|
| Azure OpenAI | Standard S0 | GPT-4o-mini (chat) + text-embedding-ada-002 (embeddings) |
| Azure AI Search | Free | Hybrid vector + BM25 index |
| Azure Blob Storage | Standard LRS | Optional Banner PDF source |

Estimated cost for dev/demo: ~$1–5/month. Use 10K TPM limits on both deployments.

---

## HTTP API

### System

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/health` | Health + model info |
| GET | `/index/stats` | Chunk count in index |
| POST | `/index/create` | Create/recreate search index |
| GET | `/debug/chunks` | List top 50 indexed chunks |

### Banner

| Method | Endpoint | Purpose |
|---|---|---|
| POST | `/banner/ask` | Q&A over all Banner release notes |
| POST | `/banner/{module}/ask` | Module-scoped Q&A — `general`, `finance`, `student`, `hr`, `ar`, `financial-aid` |
| POST | `/banner/ingest` | Ingest PDFs from `data/docs/banner` |
| GET | `/banner/blob/list` | List PDFs in Azure Blob |
| POST | `/banner/blob/sync` | Download from Blob and ingest |
| POST | `/banner/summarize/full` | Structured summary: changes, breaking, actions, compatibility |

Key request fields for `/banner/ask`: `question` (required), `module_filter`, `version_filter`, `year_filter`, `top_k`, `mode` (`local`/`web`/`hybrid`/`auto`).

### Banner Student

| Method | Endpoint | Purpose |
|---|---|---|
| POST | `/banner/student/ask` | Q&A over Student user guide |
| POST | `/banner/student/ingest` | Ingest Student user guide PDFs |
| POST | `/banner/student/procedure` | Step-by-step instructions for a task |
| POST | `/banner/student/lookup` | Definition lookup from user guide |
| POST | `/banner/student/cross-reference` | How a release change affects guide procedures |

### SOP

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/sop` | List all indexed SOPs |
| POST | `/sop/ask` | Q&A over SOPs |
| POST | `/sop/ingest` | Ingest `.docx` files from `data/docs/sop` |

### Chatbot adapter

| Method | Endpoint | Purpose |
|---|---|---|
| POST | `/chat/ask` | Main Q&A — routes by intent/source, returns `answer`, `confidence`, `escalate`, `sources` |
| POST | `/chat/intent` | Classify message → one of 6 intents |
| POST | `/chat/sentiment` | Score message frustration (0–1) |

---

## Chatbot: intent routing

| Intent | Source field | Backend | Notes |
|---|---|---|---|
| `BannerRelease` | `banner` | `/banner/ask` `module_filter=General` | Release notes |
| `BannerFinance` | `finance` | `/banner/ask` `module_filter=Finance` | Finance release notes |
| `SopQuery` | `sop` | `/sop/ask` | SOPs |
| `BannerAdmin` | `banner` | `/banner/ask` `module_filter=General` | Admin/config questions |
| `BannerUsage` | `user_guide` | `/banner/general/ask` `source_type=banner_user_guide` | How to use Banner |
| `General` | `banner` | `/banner/ask` `module_filter=General` | Fallback |

`source` field in `/chat/ask` overrides intent routing. Valid values: `banner`, `finance`, `sop`, `user_guide`, `user_guide_student`, `user_guide_finance`, `auto`.

> **Confidence note:** `sources[0].score` is the raw Azure AI Search hybrid score. Valid answers typically score 0.01–0.05 — this is normal, not low quality. See [wiki/RUNBOOK.md](wiki/RUNBOOK.md) § Score Distribution.

---

## Testing the bot

| Method | How |
|---|---|
| Botpress Studio Preview | Open [Studio](https://studio.botpress.cloud/3b6cf557-bc0a-4197-b16a-29c79706809f/flows/wf-main) → click Preview — tests draft, no publish needed |
| demo/index.html | Open in browser — tests last published version |
| curl adapter | `POST http://localhost:8080/chat/ask` with `{"message":"...","session_id":"test-1"}` |

See [wiki/BOTPRESS-SETUP.md](wiki/BOTPRESS-SETUP.md) for full flow setup and Execute Code snippets.

---

## Swagger UI

```bash
go generate ./internal/api/   # generates docs/ (gitignored)
# then: http://localhost:8000/docs/index.html
```

Bruno collection at `apis/Omnivore RAG API/` — set `base_url=http://localhost:8000`.

---

## Document ingestion

```
data/docs/
├── banner/
│   ├── general/2026/february/Banner_General_9.3.37.2_ReleaseNotes.pdf
│   ├── finance/2026/february/Banner_Finance_9.3.22_ReleaseNotes.pdf
│   └── student/2026/february/Banner_Student_9.39_ReleaseNotes.pdf
└── sop/
    ├── SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx
    └── SOP154 - Procedure - Start, Stop Axiom.docx
```

Supported: `.pdf`, `.txt`, `.md`, `.docx`. SOPs must follow the `SOP<N> - <Title>.docx` naming convention.

---

## gRPC

Runs on `:9000`. Exposes `SystemService`, `BannerService`, and `SOPService` — same functionality as the HTTP API.

```bash
go run cmd/grpc/main.go
buf generate               # regenerate after .proto changes
grpcurl -plaintext localhost:9000 list
```

Proto definitions: `proto/omnivore/v1/`. Generated code: `gen/go/` (gitignored).

---

## .env reference

```env
AZURE_OPENAI_ENDPOINT=https://<resource>.openai.azure.com/
AZURE_OPENAI_API_KEY=
AZURE_OPENAI_API_VERSION=2024-12-01-preview
AZURE_OPENAI_CHAT_DEPLOYMENT=gpt-4o-mini
AZURE_OPENAI_EMBEDDING_DEPLOYMENT=text-embedding-ada-002

AZURE_SEARCH_ENDPOINT=https://<search>.search.windows.net
AZURE_SEARCH_API_KEY=
AZURE_SEARCH_INDEX_NAME=omnivore-knowledge

AZURE_STORAGE_CONNECTION_STRING=   # optional
AZURE_STORAGE_CONTAINER_NAME=banner-release-notes

TAVILY_API_KEY=                    # optional, enables mode=web/hybrid/auto
CONFIDENCE_HIGH_THRESHOLD=0.030
CONFIDENCE_LOW_THRESHOLD=0.010

CHUNK_SIZE=500
CHUNK_OVERLAP=25
TOP_K_DEFAULT=5
API_PORT=8000
GRPC_PORT=9000
```

---

## Wiki

| Guide | What it covers |
|---|---|
| [RUNBOOK.md](wiki/RUNBOOK.md) | **Start here** — end-to-end setup for all run paths |
| [LOCAL-DEV.md](wiki/LOCAL-DEV.md) | Dev session startup, env vars, common commands |
| [DOCKER-DEV.md](wiki/DOCKER-DEV.md) | Full Docker Compose stack (backend + adapter + ngrok) |
| [BOTPRESS-SETUP.md](wiki/BOTPRESS-SETUP.md) | Botpress wiring, Execute Code snippets, testing options |
| [FLY-NGROK.md](wiki/FLY-NGROK.md) | Fly.io deploy, ngrok tunnel, secrets |
| [CHATBOT.md](wiki/CHATBOT.md) | Architecture, API surface, response shapes, user guide routing |
| [INTERNALS.md](wiki/INTERNALS.md) | Design decisions, data flow, chunking strategy |
| [TROUBLESHOOTING.md](wiki/TROUBLESHOOTING.md) | Symptoms → root cause → fix |
| [INTEGRATIONS.md](wiki/INTEGRATIONS.md) | LangGraph, n8n, MCP integration ideas |
| [OBSERVABILITY.md](wiki/OBSERVABILITY.md) | Logging, metrics, tracing |
| [UPGRADES.md](wiki/UPGRADES.md) | API hardening, RAG quality, streaming, roadmap |
| [CLAUDE_AGENTS.md](wiki/CLAUDE_AGENTS.md) | Claude agent designs over this backend |

---

## Implementation notes

- Azure OpenAI and Azure AI Search use direct REST calls — no official Go SDK (intentional for transparency)
- Azure Blob Storage uses the official Go SDK
- DOCX parsing uses only `archive/zip` and `encoding/xml`
- Single Azure AI Search index stores Banner, SOP, and user-guide documents separated by `source_type`
- gRPC reflection enabled so `grpcurl` works without `.proto` files

---

## License

MIT. Not affiliated with or endorsed by Ellucian. Banner is a trademark of Ellucian Company L.P. Do not redistribute real Banner release notes.
