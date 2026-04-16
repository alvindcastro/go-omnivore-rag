# Docker Development Guide

How to run the entire Ask Banner stack — RAG backend, adapter, and ngrok tunnel — in Docker.
No Go toolchain, no local ngrok install, no Fly.io needed for local dev or demos.

---

## Architecture

```
[demo/index.html]
       ↓  Botpress webchat widget (loads from CDN)
[Botpress Cloud]
       ↓  RAG_ADAPTER_URL = ngrok public HTTPS URL
[ngrok container]                ← port 4040 inspector UI
       ↓  http://adapter:8080    (Docker internal network)
[adapter container :8080]        ← cmd/server/main.go, Dockerfile.adapter
       ↓  http://http:8000       (Docker internal network)
[http container :8000]           ← cmd/main.go, Dockerfile
       ↓
[Azure OpenAI + Azure AI Search]
```

**Key difference from the Fly.io approach:** The adapter and backend are on the same Docker network, so `RAG_BACKEND_URL=http://http:8000` — no ngrok tunnel between them. ngrok only exposes the adapter to Botpress Cloud.

---

## Services

| Service | Image | Port | Purpose |
|---|---|---|---|
| `http` | `go-omnivore-rag` (Dockerfile) | 8000 | RAG backend — Banner ask, SOP ask, ingest |
| `grpc` | `go-omnivore-rag` (same image) | 9000 | gRPC interface (optional) |
| `adapter` | `ask-banner-adapter` (Dockerfile.adapter) | 8080 | Botpress adapter — /chat/* endpoints |
| `ngrok` | `ngrok/ngrok:latest` | 4040 (UI) | Tunnel adapter port to public HTTPS URL |

---

## Prerequisites

| Requirement | Notes |
|---|---|
| Docker Desktop | Windows/Mac — includes Compose v2 |
| `.env` file | Copy from `.env.example`, fill Azure values |
| ngrok authtoken | Free at dashboard.ngrok.com → "Your Authtoken" |

**Add your ngrok authtoken to `.env`:**
```
NGROK_AUTHTOKEN=<your-token-from-ngrok-dashboard>
```

The ngrok container reads this from the environment. Never commit the real value.

---

## Quick start

```bash
# Build images and start everything
docker compose up --build

# Or detached (background)
docker compose up --build -d
```

On first run, `--build` compiles both Go binaries. Subsequent runs without `--build` reuse cached images.

---

## Get the ngrok public URL

After `docker compose up`, the ngrok container opens a tunnel and prints the URL to stdout.
You can also query it from the ngrok API:

```bash
curl -s http://localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'
# Returns something like: https://abc123.ngrok-free.app
```

Or open the ngrok inspector in your browser: **http://localhost:4040**

---

## Wire Botpress Cloud

Set the ngrok URL as the adapter URL in Botpress Cloud:

1. Botpress Cloud → your bot → **Configuration** → **Environment Variables**
2. Set `RAG_ADAPTER_URL` = the `https://...ngrok-free.app` URL from the step above
3. Click **Save**, then **Publish** the bot

> The ngrok free-tier URL changes every time the ngrok container restarts.
> After each `docker compose up` or `docker compose restart ngrok`, repeat this step.

---

## Selective startup

You don't have to run all four services every time:

```bash
# Backend only (RAG Q&A, ingest, debug endpoints)
docker compose up http

# Backend + adapter (test /chat/* endpoints without Botpress)
docker compose up http adapter

# Full chatbot stack (backend + adapter + ngrok)
docker compose up http adapter ngrok

# Everything including gRPC
docker compose up
```

**Tip:** Bring up `http` first and let its healthcheck pass before adding other services.
`depends_on: condition: service_healthy` handles this automatically when you run all services together.

---

## Rebuild after code changes

```bash
# Rebuild only the service you changed
docker compose build adapter
docker compose up adapter --no-deps   # restart adapter without touching other services

# Rebuild backend after Go changes
docker compose build http
docker compose up http --no-deps
```

The `--no-deps` flag prevents Compose from restarting dependent services unnecessarily.

---

## Verify each layer

Run these after `docker compose up` to confirm each service is healthy:

```bash
# 1. RAG backend
curl -s http://localhost:8000/health
# Expected: {"status":"ok"}

# 2. Adapter
curl -s http://localhost:8080/health
# Expected: {"status":"ok"}

# 3. Adapter → backend route (full pipeline test)
curl -s -X POST http://localhost:8080/chat/intent \
  -H "Content-Type: application/json" \
  -d '{"message":"When is the add/drop deadline?"}' | jq .
# Expected: {"intent":"RegistrationBanner","confidence":...}

# 4. Full ask through adapter
curl -s -X POST http://localhost:8080/chat/ask \
  -H "Content-Type: application/json" \
  -d '{"message":"What changed in Banner 9.3.37?","session_id":"test-1","intent":"ReleaseSummary"}' | jq .
# Expected: {"answer":"...","confidence":...,"escalate":...,"sources":[...]}

# 5. ngrok tunnel is up
curl -s http://localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'
# Expected: https://....ngrok-free.app
```

---

## Logs

```bash
# All services
docker compose logs -f

# One service at a time
docker compose logs -f http
docker compose logs -f adapter
docker compose logs -f ngrok

# Last 50 lines only
docker compose logs --tail=50 adapter
```

---

## Common operations

```bash
# Stop all services (containers remain, can restart quickly)
docker compose stop

# Stop and remove containers (images kept, volumes kept)
docker compose down

# Stop and remove everything including volumes
docker compose down -v

# Restart a single service
docker compose restart adapter

# Check service health status
docker compose ps

# Shell into a running container
docker compose exec http sh
docker compose exec adapter sh

# Run a one-off command (e.g., check binary version)
docker compose run --rm adapter ./ask-banner --help
```

---

## Health check behaviour

Each service has a health check configured. `depends_on: condition: service_healthy` means:
- `grpc` and `adapter` won't start until `http` is healthy (backend up + responding)
- `ngrok` won't start until `adapter` is healthy (adapter built + reachable)

This prevents the "connection refused" race condition on first startup.

Healthcheck timings:

| Service | Interval | Start period | What it checks |
|---|---|---|---|
| `http` | 30s | 15s | `GET /health` on port 8000 |
| `adapter` | 15s | 10s | `GET /health` on port 8080 |
| `ngrok` | (none — no HTTP to check) | — | — |

---

## Ingesting documents inside Docker

The `data/` directory is bind-mounted into the `http` container at `/app/data`.
Drop your Banner PDFs and SOP files into `data/docs/banner/` and `data/docs/sop/` locally,
then trigger ingestion via the HTTP API:

```bash
# Ingest Banner PDFs
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{"docs_path":"data/docs/banner","overwrite":false}' | jq .

# Ingest SOP documents
curl -s -X POST http://localhost:8000/sop/ingest \
  -H "Content-Type: application/json" \
  -d '{"overwrite":false}' | jq .

# Confirm chunks are indexed
curl -s http://localhost:8000/index/stats | jq .
```

Files are read from the container's `/app/data` path which maps directly to your local `./data` directory. No container rebuild needed when you add new documents.

---

## Troubleshooting

### ngrok container exits immediately

**Symptom:** `docker compose ps` shows ngrok as `Exited`.

**Cause:** `NGROK_AUTHTOKEN` missing or invalid.

**Fix:**
```bash
# Check the logs
docker compose logs ngrok
# Expected error: "authentication failed" or "authtoken is required"

# Add token to .env
echo "NGROK_AUTHTOKEN=your-token-here" >> .env
docker compose up ngrok
```

Get your token at: https://dashboard.ngrok.com/get-started/your-authtoken

---

### adapter starts but can't reach backend

**Symptom:** `docker compose logs adapter` shows "connection refused" connecting to `http:8000`.

**Cause:** `http` service not healthy yet when adapter started, or backend crashed.

**Fix:**
```bash
docker compose ps          # check http health
docker compose logs http   # check for Azure credential errors

# Restart adapter after backend is healthy
docker compose restart adapter
```

---

### ngrok URL changed between sessions

**Symptom:** Botpress shows errors after restarting the stack.

**Cause:** Free ngrok plan assigns a new random URL on every tunnel session.

**Fix:** Get the new URL and update Botpress:
```bash
curl -s http://localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'
# Copy this URL → Botpress Cloud → Configuration → Environment Variables → RAG_ADAPTER_URL → Save → Publish
```

**Permanent fix:** Claim a static domain on ngrok free plan (one per account). Then replace the `command` in `docker-compose.yml`:
```yaml
ngrok:
  command: http adapter:8080 --domain=your-static-domain.ngrok-free.app --log stdout
```
The URL never changes after this.

---

### Image build fails on go mod download

**Symptom:** `docker compose build` fails at `go mod download` step.

**Cause:** No internet access from the Docker build context, or proxy issues.

**Fix:**
```bash
# Clear Docker build cache and try again
docker builder prune
docker compose build --no-cache http
```

---

### Port conflict — "address already in use"

**Symptom:** `docker compose up` fails with `bind: address already in use` on 8000, 8080, or 4040.

**Fix:**
```bash
# Find what's using the port (example: 8000)
netstat -ano | findstr :8000   # Windows
ss -tlnp | grep 8000           # Linux/WSL

# Stop the conflicting process, then retry
docker compose up
```

If you're running the Go backend locally alongside Docker, stop the local process first.

---

## vs. the Fly.io approach

| | Docker Compose (local) | Fly.io (cloud) |
|---|---|---|
| Backend location | Local container | Local (still via ngrok) |
| Adapter location | Local container | Fly.io cloud |
| ngrok tunnels | Adapter only | Backend (to reach localhost) |
| URL stability | Changes on restart (unless static domain) | Fly URL is permanent |
| Setup complexity | One `docker compose up` | Fly deploy + secrets + ngrok for backend |
| Good for | Local dev, demos, portfolio recording | Persistent public deployment |
| Fly.io needed? | No | Yes |

For a Loom recording or portfolio demo, Docker Compose is simpler — one command brings everything up.
For a link you can share that stays alive, Fly.io is better.

---

## Static ngrok domain (nice to know)

ngrok free plan includes **one static domain**. Claim it at:
> dashboard.ngrok.com → Cloud Edge → Domains → New Domain

Then use it in `docker-compose.yml`:
```yaml
ngrok:
  command: http adapter:8080 --domain=<your-static-domain>.ngrok-free.app --log stdout
```

With a static domain, the Botpress `RAG_ADAPTER_URL` never needs updating, even after container restarts. Set it once in Botpress Cloud and forget it.
