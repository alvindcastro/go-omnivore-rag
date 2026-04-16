# Local Development Guide

How to set up and run the full Ask Banner stack on your local machine.

---

## Architecture recap

```
[demo/index.html]         ← open in browser (static file, no server needed)
       ↓ Botpress webchat widget
[Botpress Cloud]          ← cloud-hosted, calls your adapter via HTTP
       ↓ RAG_ADAPTER_URL (https://ask-banner.fly.dev)
[ask-banner on Fly.io]    ← runs cmd/server/main.go (the adapter)
       ↓ RAG_BACKEND_URL (Fly secret → your ngrok URL)
[ngrok tunnel]            ← exposes localhost:8000 to the internet
       ↓
[go-omnivore-rag backend] ← localhost:8000, running in WSL
       ↓
[Azure OpenAI + Azure AI Search]
```

The only things that run locally are:
- The go-omnivore-rag RAG backend (port 8000)
- ngrok (tunnel into that backend)

Everything else (adapter, Botpress) runs in the cloud.

---

## Prerequisites

| Tool | Purpose | Install |
|---|---|---|
| Go 1.24+ | Build and run the backend | https://go.dev/dl |
| ngrok | Expose localhost to Fly.io | https://ngrok.com/download |
| flyctl | Fly.io CLI | `curl -L https://fly.io/install.sh \| sh` |
| `.env` | Azure credentials | Copy from `.env.example`, fill values |

**ngrok account:** Free plan is sufficient. Register at ngrok.com and run `ngrok config add-authtoken <token>` once. Free plan URLs change on every restart.

---

## First-time setup

```bash
# 1. Clone and enter repo
git clone <repo-url>
cd go-omnivore-rag

# 2. Copy env template and fill Azure values
cp .env.example .env
# Edit .env — required fields:
#   AZURE_OPENAI_ENDPOINT
#   AZURE_OPENAI_API_KEY
#   AZURE_SEARCH_ENDPOINT
#   AZURE_SEARCH_API_KEY

# 3. Download Go dependencies
go mod download

# 4. Log in to Fly (once per machine)
fly auth login

# 5. Verify backend builds
go build ./cmd/...
```

---

## Option A — Docker Compose (recommended)

Run the entire stack with a single command. No Go toolchain or local ngrok install needed.
See [DOCKER-DEV.md](DOCKER-DEV.md) for the full guide.

```bash
# Build images and start backend + adapter + ngrok
docker compose up --build

# Get the ngrok public URL (set this in Botpress Cloud as RAG_ADAPTER_URL)
curl -s http://localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'
```

## Option B — Run Go directly (dev session startup)

Every dev session, open three terminals:

```bash
# Terminal 1 — start the RAG backend (WSL path shown; adjust to your shell)
cd /mnt/c/Users/decastroa/GolandProjects/go-omnivore-rag
go run cmd/main.go
# Expected: "Starting Banner Upgrade RAG API... Listening on :8000"

# Terminal 2 — expose backend via ngrok
ngrok http 8000
# Copy the https:// Forwarding URL, e.g.:
# https://isolating-riverbank-frozen.ngrok-free.dev

# Terminal 3 — update Fly secret if ngrok URL changed (check first)
fly secrets list   # shows current RAG_BACKEND_URL
fly secrets set RAG_BACKEND_URL=https://<your-ngrok-url>
# This triggers an automatic adapter restart — no fly deploy needed
```

**Check it works:**
```bash
# Backend alive?
curl http://localhost:8000/health

# Adapter alive? (Fly.io cold-start may take ~5 s on first request)
curl https://ask-banner.fly.dev/health
# Expected: {"status":"ok"}
```

---

## Running the adapter locally (optional)

The adapter normally runs on Fly.io, but you can run it locally for faster iteration:

```bash
# Terminal 3 instead of the Fly workflow
RAG_BACKEND_URL=http://localhost:8000 PORT=8080 go run cmd/server/main.go

# Test an intent classify
curl -s -X POST http://localhost:8080/chat/intent \
  -H "Content-Type: application/json" \
  -d '{"message":"What changed in Banner 9.3.37?"}' | jq .
# Expected: {"intent":"BannerRelease","confidence":0.9}

# Test sentiment
curl -s -X POST http://localhost:8080/chat/sentiment \
  -H "Content-Type: application/json" \
  -d '{"message":"I have been waiting 3 days and nobody answers!!!"}' | jq .
# Expected: {"sentiment":"Frustrated","score":...}

# Test full ask
curl -s -X POST http://localhost:8080/chat/ask \
  -H "Content-Type: application/json" \
  -d '{"message":"What changed in Banner 9.3.37?","session_id":"test-1","intent":"BannerRelease"}' | jq .
```

When running the adapter locally, temporarily point Botpress at your ngrok URL for the adapter too (or skip Botpress and test with curl).

---

## Running tests

```bash
# Full suite (recommended)
go test ./... -v -race

# Adapter layer only
go test ./internal/adapter/... -v

# Intent classifier only
go test ./internal/intent/... -v

# Sentiment analyzer only
go test ./internal/sentiment/... -v

# HTTP handlers only
go test ./api/... -v
```

All tests use `httptest` mocks — no live backend needed.

> **Note on -race:** CGO_ENABLED=1 required on Windows. If the race detector isn't available in your shell, drop `-race` for a quick check and add it back in CI.

---

## Opening the demo page

`demo/index.html` is a static file. Open it directly in a browser:

```
file:///C:/Users/decastroa/GolandProjects/go-omnivore-rag/demo/index.html
```

Or serve it locally so Botpress CORS doesn't complain:
```bash
# Python (any modern version)
python -m http.server 3000 --directory demo
# Open http://localhost:3000
```

The Botpress widget loads from `cdn.botpress.cloud` and connects to Botpress Cloud — the page just needs to be open in a browser. The widget works even from `file://`.

---

## Ingesting documents (backend)

```bash
# Ingest Banner release note PDFs from data/docs/banner/
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{"docs_path":"data/docs/banner","overwrite":false}' | jq .

# Ingest SOP documents
curl -s -X POST http://localhost:8000/sop/ingest \
  -H "Content-Type: application/json" \
  -d '{"overwrite":false}' | jq .

# Check index stats after ingestion
curl -s http://localhost:8000/index/stats | jq .

# List indexed SOPs
curl -s http://localhost:8000/sop | jq .

# Peek at chunks (first 50)
curl -s http://localhost:8000/debug/chunks | jq '.[0:3]'
```

---

## Environment variable reference

### Backend (`cmd/main.go` — loaded from `.env`)

| Variable | Required | Default | Notes |
|---|---|---|---|
| `AZURE_OPENAI_ENDPOINT` | Yes | — | `https://<resource>.openai.azure.com/` |
| `AZURE_OPENAI_API_KEY` | Yes | — | |
| `AZURE_OPENAI_CHAT_DEPLOYMENT` | No | `gpt-4o-mini` | |
| `AZURE_OPENAI_EMBEDDING_DEPLOYMENT` | No | `text-embedding-ada-002` | |
| `AZURE_SEARCH_ENDPOINT` | Yes | — | `https://<resource>.search.windows.net` |
| `AZURE_SEARCH_API_KEY` | Yes | — | |
| `AZURE_SEARCH_INDEX_NAME` | No | `omnivore-knowledge` | |
| `API_PORT` | No | `8000` | |
| `TOP_K_DEFAULT` | No | `5` | chunks returned per query |
| `CHUNK_SIZE` | No | `1000` | chars per chunk |
| `CHUNK_OVERLAP` | No | `150` | overlap between consecutive chunks |

### Adapter (`cmd/server/main.go` — set as Fly secret or local env)

| Variable | Required | Default | Notes |
|---|---|---|---|
| `RAG_BACKEND_URL` | Yes | — | ngrok URL (prod) or `http://localhost:8000` (local) |
| `PORT` | No | `8080` | adapter listen port |

---

## Project structure (chatbot-relevant paths)

```
go-omnivore-rag/
├── cmd/
│   ├── main.go            ← RAG backend entry point (Azure deps, port 8000)
│   └── server/
│       └── main.go        ← Adapter entry point (no Azure deps, port 8080)
├── api/
│   ├── handlers.go        ← /chat/ask, /chat/intent, /chat/sentiment, /health
│   └── handlers_test.go
├── internal/
│   ├── adapter/
│   │   ├── client.go      ← HTTP client → go-omnivore-rag backend
│   │   └── client_test.go
│   ├── intent/
│   │   ├── classifier.go  ← keyword intent classifier (6 intents)
│   │   └── classifier_test.go
│   └── sentiment/
│       ├── analyzer.go    ← rule-based sentiment (Positive/Neutral/Frustrated)
│       └── analyzer_test.go
├── demo/
│   └── index.html         ← Botpress widget embed page
├── Dockerfile             ← RAG backend image (Azure + Swagger)
├── Dockerfile.adapter     ← Adapter image (lean, no Azure deps)
└── fly.toml               ← Fly.io config → builds Dockerfile.adapter
```

---

## Common daily tasks

| Task | Command |
|---|---|
| Check Fly app status | `fly status` |
| Tail live adapter logs | `fly logs` |
| Update ngrok secret | `fly secrets set RAG_BACKEND_URL=https://<url>` |
| SSH into Fly machine | `fly ssh console` |
| List Fly secrets (keys only) | `fly secrets list` |
| Force redeploy adapter | `fly deploy` |
| Build adapter locally | `go build ./cmd/server/...` |
| Build backend locally | `go build ./cmd/...` |
