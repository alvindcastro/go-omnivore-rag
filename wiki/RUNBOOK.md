# Runbook — How to Run Everything

Step-by-step guide to get the full Ask Banner stack running from scratch.
Choose one of three paths based on what you need.

---

## Which path should I use?

| Path | Best for | Requires |
|---|---|---|
| [A — Docker Compose](#path-a--docker-compose-recommended) | Local dev, demos, portfolio recording | Docker Desktop, Azure creds, ngrok token |
| [B — Local Go + Fly.io](#path-b--local-go--flyio-cloud-adapter) | Persistent public URL to share | Go 1.24+, flyctl, Azure creds, ngrok |
| [C — Fully Local Go](#path-c--fully-local-no-cloud) | Quick backend tests, no chatbot needed | Go 1.24+, Azure creds |

All paths share the same **one-time setup** below.

---

## One-time setup

### 1. Clone the repo

```bash
git clone <repo-url>
cd go-omnivore-rag
```

### 2. Configure environment variables

```bash
cp .env.example .env
```

Open `.env` and fill in your Azure credentials (minimum required):

```env
AZURE_OPENAI_ENDPOINT=https://<your-resource>.openai.azure.com/
AZURE_OPENAI_API_KEY=<your-key>
AZURE_SEARCH_ENDPOINT=https://<your-search>.search.windows.net
AZURE_SEARCH_API_KEY=<your-search-admin-key>
```

Optional (needed for web search mode and the chatbot stack):

```env
TAVILY_API_KEY=tvly-<your-key>
NGROK_AUTHTOKEN=<your-ngrok-authtoken>    # get at dashboard.ngrok.com
```

> Never commit `.env` — it is gitignored.

### 3. Create the Azure AI Search index (first time only)

Once the backend is running (any path), hit this endpoint once:

```bash
curl -s -X POST http://localhost:8000/index/create
# Expected: {"status":"index created"} or {"status":"index already exists"}
```

---

## Path A — Docker Compose (recommended)

Runs backend + adapter + ngrok in one command. No Go toolchain needed locally.

### Prerequisites

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) (includes Compose v2)
- `.env` filled (see One-time setup above)
- `NGROK_AUTHTOKEN` set in `.env` (required for the ngrok container)

### Step 1 — Build and start everything

```bash
docker compose up --build
```

First run compiles both Go binaries — takes 1–2 minutes.
Subsequent runs without `--build` reuse cached images and start in seconds.

### Step 2 — Wait for health checks

Watch for these lines in the logs:

```
http     | Starting Banner Upgrade RAG API... Listening on :8000
adapter  | Ask Banner adapter starting — port 8080 → backend http://http:8000
ngrok    | started tunnel ... url=https://....ngrok-free.app
```

### Step 3 — Get the ngrok public URL

```bash
curl -s http://localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'
# Returns: https://abc123.ngrok-free.app
```

Or open the ngrok inspector: **http://localhost:4040**

### Step 4 — Wire Botpress Cloud

1. Botpress Cloud → your bot → **Configuration** → **Environment Variables**
2. Set `RAG_ADAPTER_URL` = the `https://....ngrok-free.app` URL from Step 3
3. Click **Save** → **Publish**

> The ngrok free-tier URL changes every time the container restarts. Repeat this step after each `docker compose up`.
> To avoid this: claim a [static ngrok domain](#static-ngrok-domain-tip) (free, one per account).

### Step 5 — Verify each layer

```bash
# RAG backend
curl -s http://localhost:8000/health
# Expected: {"status":"ok"}

# Adapter
curl -s http://localhost:8080/health
# Expected: {"status":"ok"}

# Intent classify (adapter → backend round-trip)
curl -s -X POST http://localhost:8080/chat/intent \
  -H "Content-Type: application/json" \
  -d '{"message":"What changed in Banner 9.3.37?"}' | jq .
# Expected: {"intent":"BannerRelease","confidence":...}

# Full ask (adapter → backend → Azure)
curl -s -X POST http://localhost:8080/chat/ask \
  -H "Content-Type: application/json" \
  -d '{"message":"What changed in Banner 9.3.37?","session_id":"test-1","intent":"BannerRelease"}' | jq .
# Expected: {"answer":"...","confidence":...,"escalate":...,"sources":[...]}
```

### Step 6 — Open the demo page

```
demo/index.html
```

Open it in a browser (file:// works, or serve it locally):

```bash
python -m http.server 3000 --directory demo
# Open http://localhost:3000
```

The Botpress widget connects to Botpress Cloud automatically — the page just needs to be open.

### Daily startup (after first-time setup)

```bash
docker compose up
# Get ngrok URL if it changed:
curl -s http://localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'
# Update Botpress if URL changed → Save → Publish
```

### Stop the stack

```bash
docker compose stop          # pause (fast restart later)
docker compose down          # remove containers (images kept)
docker compose down -v       # nuclear — removes containers + volumes
```

---

## Path B — Local Go + Fly.io (cloud adapter)

The RAG backend runs locally. The adapter runs on Fly.io (free tier). ngrok bridges your localhost to Fly.

```
[Botpress Cloud]
      ↓  RAG_ADAPTER_URL
[ask-banner.fly.dev]     ← adapter on Fly.io
      ↓  RAG_BACKEND_URL (Fly secret → ngrok URL)
[ngrok tunnel]
      ↓
[localhost:8000]         ← go-omnivore-rag running locally
      ↓
[Azure OpenAI + Azure AI Search]
```

### Prerequisites

- [Go 1.24+](https://go.dev/dl)
- [ngrok](https://ngrok.com/download) — run `ngrok config add-authtoken <token>` once
- [flyctl](https://fly.io/docs/flyctl/install/) — run `fly auth login` once
- `.env` filled (see One-time setup above)

### First-time Fly deploy

```bash
# 1. Set the backend URL secret (ngrok URL filled in after you start ngrok below)
fly secrets set RAG_BACKEND_URL=https://<your-ngrok-url>

# 2. Deploy the adapter image
fly deploy
# Deploys Dockerfile.adapter (~15 MB Alpine image)

# 3. Verify
curl https://ask-banner.fly.dev/health
# Expected: {"status":"ok"}
```

### Every dev session — 3 terminals

```bash
# Terminal 1 — start the RAG backend
cd /mnt/c/Users/decastroa/GolandProjects/go-omnivore-rag   # WSL path
go run cmd/main.go
# Expected: "Listening on :8000"

# Terminal 2 — expose backend via ngrok
ngrok http 8000
# Copy the https:// Forwarding URL, e.g.:
# https://isolating-riverbank-frozen.ngrok-free.dev

# Terminal 3 — update Fly secret only if the ngrok URL changed
fly secrets list                                        # check current value first
fly secrets set RAG_BACKEND_URL=https://<ngrok-url>    # triggers automatic restart
```

> `fly secrets set` restarts the adapter automatically — no `fly deploy` needed.

### Wire Botpress Cloud (one time)

1. Botpress Cloud → your bot → **Configuration** → **Environment Variables**
2. Set `RAG_ADAPTER_URL` = `https://ask-banner.fly.dev`
3. Click **Save** → **Publish**

This URL is permanent (Fly.io subdomain never changes).

### Verify

```bash
curl http://localhost:8000/health           # backend
curl https://ask-banner.fly.dev/health      # adapter on Fly

curl -s -X POST https://ask-banner.fly.dev/chat/intent \
  -H "Content-Type: application/json" \
  -d '{"message":"how do I register for a course?"}' | jq .
```

---

## Path C — Fully Local (no cloud)

Backend + adapter run locally. No Fly.io, no Botpress needed. Use curl to test.

### Step 1 — Start the RAG backend

```bash
go run cmd/main.go
# Expected: "Listening on :8000"
```

### Step 2 — Start the adapter

```bash
RAG_BACKEND_URL=http://localhost:8000 PORT=8080 go run cmd/server/main.go
# Expected: "Ask Banner adapter starting — port 8080 → backend http://localhost:8000"
```

### Step 3 — Test the endpoints

```bash
# Intent classify
curl -s -X POST http://localhost:8080/chat/intent \
  -H "Content-Type: application/json" \
  -d '{"message":"I need to pay my tuition"}' | jq .

# Sentiment
curl -s -X POST http://localhost:8080/chat/sentiment \
  -H "Content-Type: application/json" \
  -d '{"message":"I have been waiting 3 days and nobody answers!!!"}' | jq .

# Full ask
curl -s -X POST http://localhost:8080/chat/ask \
  -H "Content-Type: application/json" \
  -d '{"message":"What changed in Banner 9.3.37?","session_id":"test-1","intent":"General"}' | jq .

# Direct backend ask (bypasses adapter)
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"What are the prerequisites for Banner General 9.3.37.2?","top_k":5}' | jq .
```

---

## Ingest documents (all paths)

Drop files into the right folder first:

```
data/docs/
├── banner/general/2026/february/   ← Banner release note PDFs
│                                      e.g. Banner_General_9.3.37.2_ReleaseNotes.pdf
└── sop/                            ← SOP .docx files
                                       e.g. SOP122 - Smoke Test Post Banner Upgrade.docx
```

Then trigger ingestion via the backend API:

```bash
# Ingest Banner PDFs
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{"docs_path":"data/docs/banner","overwrite":false}' | jq .

# Ingest SOP documents
curl -s -X POST http://localhost:8000/sop/ingest \
  -H "Content-Type: application/json" \
  -d '{"overwrite":false}' | jq .

# Confirm it worked
curl -s http://localhost:8000/index/stats | jq .
curl -s http://localhost:8000/sop | jq .
```

---

## Run tests

```bash
# Full suite
go test ./... -v

# By package
go test ./internal/adapter/... -v
go test ./internal/intent/... -v
go test ./internal/sentiment/... -v
go test ./api/... -v
```

All tests use `httptest` mocks — no live backend or Azure needed.

> `-race` requires CGO_ENABLED=1. On Windows without CGO, omit `-race`.

---

## Azure AI Search Score Distribution

Azure AI Search returns raw hybrid scores (BM25 + semantic re-ranker), **not** normalized
0–1 confidence values. These scores are index-specific and corpus-size-dependent.

**Observed range for this index** (update after each major ingestion):

| Category | Score range | Example |
|----------|------------|---------|
| Valid answer with sources | 0.01–0.05 | `"What changed in Banner?"` → 0.033 |
| No results (nothing indexed) | 0 | `"Banner 9.3.37 specific"` → 0 |

**Escalation logic** (`internal/adapter/client.go:mapResponse`):

```
escalate = retrieval_count == 0   ← hard gate (reliable binary)
        || confidence < floor      ← soft guard against near-zero noise
```

**Current floor:** `0.01` (interim — pending Phase A calibration from PLAN.md)

**Important:** A score of 0.033 with sources present is a **good answer**. Do not raise the floor
above 0.05 without running Agent 9 (Confidence Calibration) first — see `wiki/CLAUDE_AGENTS.md`.

To re-run calibration manually:
```bash
# Sample known-good query and record score
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"What changed in Banner General?","module_filter":"General","top_k":3}' \
  | jq '{retrieval_count:.retrieval_count, score:.sources[0].score, answer:.answer[:80]}'
```

---

## Swagger UI

Generate docs once, then access the interactive API explorer:

```bash
# Generate (only needed after pulling handler changes)
go generate ./internal/api/

# Start the backend
go run cmd/main.go

# Open in browser
# http://localhost:8000/docs/index.html
```

---

## Static ngrok domain tip

ngrok free plan includes **one static domain** — claim it so you never have to update Botpress.

1. Go to dashboard.ngrok.com → **Cloud Edge** → **Domains** → **New Domain**
2. Copy your static domain (e.g. `your-name.ngrok-free.app`)

**For Docker Compose** — update `docker-compose.yml`:
```yaml
ngrok:
  command: http adapter:8080 --domain=your-name.ngrok-free.app --log stdout
```

**For local ngrok** — run:
```bash
ngrok http 8000 --domain=your-name.ngrok-free.app
```

Set the URL once in Botpress Cloud. It never changes.

---

## Troubleshooting quick reference

| Symptom | Fix |
|---|---|
| `{"status":"ok"}` missing from `/health` | Backend not started or crashed — check terminal output |
| ngrok container exits immediately | `NGROK_AUTHTOKEN` missing or invalid in `.env` |
| Adapter can't reach backend | `docker compose logs http` — check for Azure credential errors |
| Botpress shows no answer | ngrok URL changed → update `RAG_ADAPTER_URL` in Botpress Cloud → Publish |
| `index/create` returns 409 | Index already exists — safe to ignore, or `overwrite:true` |
| Azure auth error on startup | Check `AZURE_OPENAI_ENDPOINT` and `AZURE_OPENAI_API_KEY` in `.env` |

Full troubleshooting guide: [TROUBLESHOOTING.md](TROUBLESHOOTING.md)

---

## Related docs

| Doc | Content |
|---|---|
| [LOCAL-DEV.md](LOCAL-DEV.md) | Dev environment details, env var reference, Fly.io commands |
| [DOCKER-DEV.md](DOCKER-DEV.md) | Docker Compose deep-dive, health checks, selective startup |
| [BOTPRESS-SETUP.md](BOTPRESS-SETUP.md) | Flow design, Execute Code snippets, widget config |
| [FLY-NGROK.md](FLY-NGROK.md) | Fly.io app IDs, secrets, deploy checklist |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | Symptoms → root cause → fix for every layer |
| [CHATBOT.md](CHATBOT.md) | Architecture, intent routing, TDD phases |
