# Troubleshooting Guide

Symptoms → root cause → fix. Organized by layer from top (Botpress) to bottom (Azure).

---

## Quick diagnostic sequence

Before chasing a specific symptom, run this top-to-bottom check:

```bash
# 1. Backend alive?
curl -s http://localhost:8000/health
# Expected: {"status":"ok"}

# 2. ngrok tunnel alive?
curl -s https://<ngrok-url>/health
# Expected: {"status":"ok"}   (same backend, different URL)

# 3. Adapter alive?
curl -s https://ask-banner.fly.dev/health
# Expected: {"status":"ok"}

# 4. Adapter can reach backend?
curl -s -X POST https://ask-banner.fly.dev/chat/intent \
  -H "Content-Type: application/json" \
  -d '{"message":"test"}' | jq .
# Expected: {"intent":"General","confidence":0}
```

If any layer fails, fix that layer before checking higher ones.

---

## Layer 1 — RAG Backend (localhost:8000)

### Backend won't start

**Symptom:** `log.Fatalf: Required environment variable "AZURE_OPENAI_ENDPOINT" is not set`

**Cause:** `.env` file missing or incomplete.

**Fix:**
```bash
cp .env.example .env
# Fill in AZURE_OPENAI_ENDPOINT, AZURE_OPENAI_API_KEY,
# AZURE_SEARCH_ENDPOINT, AZURE_SEARCH_API_KEY
go run cmd/main.go
```

---

### `/banner/ask` returns empty sources

**Symptom:** `retrieval_count: 0`, `sources: []`

**Cause A:** No documents ingested yet.
```bash
curl -s http://localhost:8000/index/stats | jq .
# If document_count == 0, ingest first
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{"docs_path":"data/docs/banner","overwrite":false}'
```

**Cause B:** Wrong search index name.
```bash
# Check what index your .env points at
grep AZURE_SEARCH_INDEX_NAME .env
# Must match the index you created in Azure Portal
```

**Cause C:** Query too vague — Azure Search RRF scores all come in below threshold.
```bash
# Try a very specific query with known terms from your documents
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"Banner 9.3.37","top_k":10}' | jq '{count:.retrieval_count,score:.sources[0].score}'
```

---

### `/banner/ask` returns 500

**Symptom:** HTTP 500, `"error": "internal server error"` or JSON decode error in logs.

**Cause:** Azure OpenAI quota exceeded or deployment name mismatch.
```bash
# Check deployment name matches Azure Portal → OpenAI resource → Deployments
grep AZURE_OPENAI_CHAT_DEPLOYMENT .env
# Default is "gpt-4o-mini" — must match exactly (case-sensitive)
```

---

### Ingestion stalls or hangs

**Symptom:** Ingest request takes >2 minutes with no response.

**Cause:** Large PDF, embedding rate limit, or Azure Blob Storage timeout.

**Fix:**
```bash
# Ingest with smaller batches — use start_page/end_page
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{"docs_path":"data/docs/banner","overwrite":false,"start_page":1,"end_page":50}'
```

**Nice to know:** Azure OpenAI embedding rate limits are per-minute token counts. If you're ingesting many large PDFs, space them out or reduce chunk size via `CHUNK_SIZE` env var.

---

## Layer 2 — ngrok tunnel

### ngrok URL changed — adapter returns connection errors

**Symptom:** `fly logs` shows `dial tcp: connection refused` or `context deadline exceeded`.

**Cause:** ngrok was restarted; free-tier URL changed.

**Fix:**
```bash
# Get new URL from ngrok terminal output, then:
fly secrets set RAG_BACKEND_URL=https://<new-ngrok-url>
# Fly auto-restarts the adapter — no deploy needed
# Verify:
fly logs  # watch for "Ask Banner adapter starting"
```

**Nice to know:** The ngrok dashboard at `http://localhost:4040` shows all tunnel activity including request/response pairs. Use it to confirm traffic is reaching your local backend.

---

### ngrok tunnel connects but requests hang

**Symptom:** `curl https://<ngrok-url>/health` returns after 30 s with a timeout.

**Cause A:** go-omnivore-rag backend isn't running.
```bash
# Check if port 8000 is listening (WSL/Linux)
ss -tlnp | grep 8000
# Windows
netstat -ano | findstr :8000
```

**Cause B:** Firewall blocking ngrok agent's outbound connection.

**Cause C:** ngrok free plan: if you have >1 concurrent connection (e.g., Fly.io + your curl test), the second one may be queued. Free plan allows 1 simultaneous tunnel.

---

### ngrok shows "ERR_NGROK_3200 — account limit"

**Cause:** Free plan: only 1 active tunnel at a time. You have multiple ngrok processes running.

**Fix:**
```bash
# Kill all ngrok processes
pkill ngrok   # Linux/WSL
# Then start a single tunnel
ngrok http 8000
```

---

## Layer 3 — Fly.io adapter

### Fly adapter cold start — first request times out in Botpress

**Symptom:** First message after a period of inactivity gets no response from Botpress.

**Cause:** `auto_stop_machines = 'stop'` in `fly.toml` — Fly stops the machine after idle period. `auto_start_machines = true` restarts it on first request, but that takes ~3–5 seconds. The Botpress Execute Code node likely has a shorter timeout.

**Fix options:**
- Set `min_machines_running = 1` in `fly.toml` to keep one instance always warm (costs ~$2/mo on shared-cpu-1x).
- Or extend the axios timeout in Botpress Execute Code: `axios.post(..., { timeout: 15000 })`.
- Or send a warmup request before the user message (POST /health as a ping).

---

### `fly deploy` fails — build error

**Symptom:** Build log shows Go compilation error.

**Common cause:** Missing `go.sum` entry (dependency added but not committed).
```bash
go mod tidy
go mod download
fly deploy
```

---

### `fly deploy` succeeds but `/health` returns 502

**Symptom:** Fly deploy reports success, but `https://ask-banner.fly.dev/health` returns 502 Bad Gateway.

**Cause:** The app started but crashed before the health check passed. Check logs:
```bash
fly logs
```

**Most likely causes:**
1. `RAG_BACKEND_URL` secret not set → adapter crashes on startup with fatal log.
2. Port mismatch — adapter listening on a different port than `internal_port` in `fly.toml`. Both must be `8080`.
3. Binary not found — `Dockerfile.adapter` build failed silently.

---

### Fly logs show adapter started but Botpress gets timeouts

**Symptom:** `fly logs` shows requests arriving, adapter logs show it forwarded to the backend, but Botpress shows an error.

**Cause:** The 30 s `http.Client` timeout in `adapter/client.go` plus network latency exceeds Botpress's Execute Code timeout (default ~10 s).

**Fix:** Reduce `top_k` to lower backend latency, or adjust Botpress Execute Code timeout:
```javascript
// In Botpress Execute Code node
const r = await axios.post(`${RAG}/chat/ask`, payload, { timeout: 25000 });
```

---

### `fly secrets set` triggers restart but app stays unhealthy

**Symptom:** After updating `RAG_BACKEND_URL`, `fly status` shows machine restarting in a loop.

**Cause:** New ngrok URL is unreachable (ngrok session expired or wrong URL copied).

**Fix:**
```bash
# Confirm ngrok is running and URL is correct
curl https://<ngrok-url>/health
# If 404 or timeout, restart ngrok and get fresh URL
fly secrets set RAG_BACKEND_URL=https://<corrected-url>
```

---

## Layer 4 — Botpress Cloud

### Botpress Execute Code node returns "Cannot read property 'data' of undefined"

**Cause:** axios call threw an error (network issue, 4xx/5xx from adapter) and `r` is undefined.

**Fix:** Wrap the call in try/catch and inspect the error:
```javascript
try {
  const r = await axios.post(`${RAG}/chat/ask`, payload, { timeout: 20000 });
  workflow.answer = r.data.answer;
} catch (err) {
  workflow.answer = 'Service unavailable. Please try again.';
  console.error('ask error:', err.message, err.response?.data);
}
```

---

### `process.env.RAG_ADAPTER_URL` is undefined in Execute Code

**Cause:** Environment variable not set in Botpress Cloud.

**Fix:** Botpress Cloud → your bot → **Configuration** → **Environment Variables** → add:
```
Key:   RAG_ADAPTER_URL
Value: https://ask-banner.fly.dev
```
Then re-publish the bot (top-right "Publish" button). Environment variables are only picked up on publish, not on save.

---

### Botpress widget doesn't appear on demo/index.html

**Symptom:** Page loads but no chat bubble.

**Cause A:** Wrong `botId` or `clientId` in the `<script>` block. Both must be `3b6cf557-bc0a-4197-b16a-29c79706809f`.

**Cause B:** Bot is unpublished. Every time you make flow changes in Botpress Studio, you must hit **Publish** or the widget serves the last published version (which may have no flow).

**Cause C:** Browser console shows CORS error. Open DevTools → Console. If Botpress CDN is blocked (e.g., corporate proxy), the script load fails silently.

**Cause D:** Script tag missing or in the `<head>` instead of before `</body>`. The `inject.js` script must load before `window.botpressWebChat.init()` is called.

---

### Botpress widget appears but messages get no response

**Symptom:** User types, message shows as sent, but no reply ever comes.

**Checklist:**
1. Is the bot published? (Check Botpress Studio — publish button)
2. Is the flow wired from Start → your Execute Code nodes? (Check in Studio)
3. Did you set `RAG_ADAPTER_URL` in Botpress Environment Variables AND re-publish?
4. Is the adapter reachable? `curl https://ask-banner.fly.dev/health`
5. Check Botpress Logs: Studio → Logs tab → filter by your bot

---

### Intent always returns "General"

**Symptom:** Every message classifies as General regardless of content.

**Cause A:** Intent field isn't being passed to `/chat/ask`. Check the Execute Code node:
```javascript
// intent must come from a previous /chat/intent call
const intentResp = await axios.post(`${RAG}/chat/intent`, { message: event.preview });
workflow.intent = intentResp.data.intent;

// Then in the ask node:
const r = await axios.post(`${RAG}/chat/ask`, {
  message: event.preview,
  session_id: event.botId + '-' + event.userId,
  intent: workflow.intent   // ← this line must be present
});
```

**Cause B:** Message doesn't contain any classifier keywords. Test the intent endpoint directly:
```bash
curl -s -X POST https://ask-banner.fly.dev/chat/intent \
  -H "Content-Type: application/json" \
  -d '{"message":"how do I register?"}' | jq .
```

---

## Layer 5 — Azure (OpenAI + AI Search)

### Azure OpenAI returns 429 (rate limited)

**Symptom:** Backend logs show `429 Too Many Requests`, response is slow or nil.

**Cause:** Tokens-per-minute (TPM) quota on the deployment exceeded.

**Fix options:**
- Reduce `TOP_K_DEFAULT` in `.env` → fewer chunks → fewer tokens per call.
- Reduce `CHUNK_SIZE` → smaller chunks → fewer tokens per chunk.
- Upgrade Azure OpenAI deployment quota in Azure Portal.
- Add delays between ingestion batches.

```bash
# Temporarily lower top_k for testing
curl -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"test","top_k":2}'
```

---

### Azure AI Search returns 403

**Symptom:** Backend starts but all search calls return 403.

**Cause A:** `AZURE_SEARCH_API_KEY` is wrong or expired.
**Cause B:** Key is a Query key (read-only) but the backend is trying to create the index. Use an Admin key.

```bash
# Check which key type you have in .env
# Azure Portal → Search resource → Keys:
#   Admin keys: create/delete indexes, write documents
#   Query keys: read-only search — NOT sufficient for ingestion
```

---

### Azure AI Search index doesn't exist yet

**Symptom:** Backend starts but first query returns `404 index not found`.

**Fix:**
```bash
curl -s -X POST http://localhost:8000/index/create | jq .
# Then ingest documents
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{"docs_path":"data/docs/banner","overwrite":false}'
```

---

## Confidence and escalation

### `escalate` is always `true`

**Symptom:** Every response has `"escalate": true`, even for questions the index should answer well.

**Root causes (check in order):**

1. **No documents ingested** — `retrieval_count == 0` always sets `escalate = true`.
   ```bash
   curl -s http://localhost:8000/index/stats | jq .document_count
   ```

2. **Azure Search scores are hybrid RRF scores**, which are typically in the range `0.005–0.05`, NOT `0–1`. If your indexed documents return these low raw scores, confidence will always be `< 0.5`.

   > **Nice to know:** The adapter sets `confidence = sources[0].score` directly. Azure AI Search hybrid (BM25 + vector) returns RRF-normalized scores, usually `0.01–0.06`. If your search index uses semantic reranking, scores can reach `0.5–1.0`. Without semantic reranking enabled, `escalate` will almost always be `true`.

   **Fix options:**
   - Enable semantic reranking in Azure AI Search (requires Standard tier).
   - Or lower the escalation threshold in `internal/adapter/client.go` from `0.5` to `0.03` to match RRF score range.
   - Or normalize scores in `mapResponse()` before comparing.

3. **Wrong index** — queries are hitting an empty or different index.
   ```bash
   curl -s http://localhost:8000/debug/chunks | jq '.[0].document_title'
   # Should return a recognizable document name
   ```

---

### Sentiment always returns "Neutral"

**Symptom:** Even `"I've been waiting forever and this is broken!!!!"` returns Neutral.

**Cause:** The message being analyzed is `event.preview` (the display-safe version) rather than the raw message body, and Botpress may truncate or sanitize it.

**Fix:** Check what you're passing to `/chat/sentiment`:
```javascript
// Log the message before sending
console.log('sentiment input:', event.preview);
const r = await axios.post(`${RAG}/chat/sentiment`, { message: event.preview });
```

If `event.preview` is being sanitized, try `event.payload.text` instead.

---

## Test suite issues

### Race condition failures (`-race`)

**Symptom:** `go test ./... -v -race` fails intermittently with `DATA RACE` on Windows.

**Cause:** CGO_ENABLED=0 disables the race detector on Windows. Build without `-race` to confirm tests pass functionally:
```bash
go test ./... -v
```

For CI (GitHub Actions, Ubuntu), `-race` works normally.

---

### Tests compile but all fail with "connection refused"

**Symptom:** Tests in `internal/adapter/...` all fail with connection refused.

**Cause:** Tests are using a live backend URL instead of `httptest.NewServer`. Check that no test hardcodes `localhost:8000`.

All adapter tests use `httptest.NewServer` — they should never need a live backend.

---

## Useful one-liners

```bash
# What version of Banner docs are indexed?
curl -s http://localhost:8000/debug/chunks | jq '[.[] | .banner_version] | unique'

# Full ask with all response fields
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"What changed in Banner 9.3.37?","top_k":3}' | jq .

# Check Fly app machine state
fly machine list

# Destroy and recreate Fly machine (last resort)
fly machine destroy <id>
fly deploy

# ngrok inspect — see all tunnel requests in your terminal
# (ngrok web UI also shows this at http://localhost:4040)
```
