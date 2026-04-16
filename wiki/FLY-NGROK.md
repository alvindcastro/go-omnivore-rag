# Ask Banner — Deployment & Configuration Notes

## Fly.io App

| Field | Value |
|---|---|
| App name | `ask-banner` |
| Public URL | `https://ask-banner.fly.dev` |
| Admin URL | `https://fly.io/apps/ask-banner` |
| Region | Ashburn, Virginia (US) — `iad` |
| Machine | shared-cpu-1x, 1GB RAM |
| Organization | personal |

---

## Botpress Cloud

| Field | Value |
|---|---|
| Workspace ID | `wkspace_01KPA7D6WYGBEWGQ2MTCCPTFFA` |
| Bot ID | `3b6cf557-bc0a-4197-b16a-29c79706809f` |
| Bot dashboard | `https://app.botpress.cloud/workspaces/wkspace_01KPA7D6WYGBEWGQ2MTCCPTFFA/bots/3b6cf557-bc0a-4197-b16a-29c79706809f/overview` |

### Environment variable set in Botpress Cloud

| Key | Value |
|---|---|
| `RAG_ADAPTER_URL` | `https://ask-banner.fly.dev` |

**Where to set:** Botpress Cloud → your bot → Configuration → Environment Variables

---

## Fly.io Secrets

Managed via `fly secrets set`. Never commit these values to the repo.

| Secret | Description |
|---|---|
| `RAG_BACKEND_URL` | URL of the local go-omnivore-rag backend, exposed via ngrok |

### Current ngrok session
```
Account:  alvin.dcastro@gmail.com (Free plan)
URL:      https://isolating-riverbank-frozen.ngrok-free.dev → http://localhost:8000
```

> **Note:** Free ngrok URLs change on every restart. When the URL changes, update the secret:
> ```bash
> fly secrets set RAG_BACKEND_URL=https://<new-ngrok-url>
> ```
> `fly secrets set` triggers an automatic app restart — no `fly deploy` needed.

---

## Local Dev Startup Sequence

Every dev session, run these in order:

```bash
# Terminal 1 — start go-omnivore-rag RAG backend (requires .env with Azure creds)
cd /mnt/c/Users/decastroa/GolandProjects/go-omnivore-rag
go run cmd/main.go
# Expected: "Starting Banner Upgrade RAG API... Listening on :8000"

# Terminal 2 — expose backend via ngrok
ngrok http 8000
# Copy the https:// Forwarding URL shown in ngrok output

# Terminal 3 — update Fly secret only if the ngrok URL changed
fly secrets list                                        # check current value first
fly secrets set RAG_BACKEND_URL=https://<ngrok-url>    # triggers automatic restart
```

> **Note:** `cmd/server/main.go` is the adapter (Fly.io). `cmd/main.go` is the RAG backend (local). Don't confuse the two.

---

## Fly.io Deploy Commands

```bash
# First deploy / redeploy after code changes
fly deploy

# Check running status
fly status

# Tail live logs
fly logs

# SSH into the running machine
fly ssh console

# List current secrets (keys only, values hidden)
fly secrets list

# Update a secret (triggers automatic restart)
fly secrets set KEY=value
```

---

## Webchat Embed (demo/index.html)

```html
<script src="https://cdn.botpress.cloud/webchat/v2/inject.js"></script>
<script>
  window.botpressWebChat.init({
    botId: '3b6cf557-bc0a-4197-b16a-29c79706809f',
    hostUrl: 'https://cdn.botpress.cloud/webchat/v2',
    messagingUrl: 'https://messaging.botpress.cloud',
    clientId: '3b6cf557-bc0a-4197-b16a-29c79706809f',
    botName: 'Ask Banner',
    showPoweredBy: false,
    enableConversationDeletion: true,
  });
</script>
```

---

## Architecture (current state)

```
[Botpress Cloud widget]
        ↓  axios (process.env.RAG_ADAPTER_URL)
[ask-banner.fly.dev]         ← Fly.io free tier
        ↓  RAG_BACKEND_URL (Fly secret)
[ngrok tunnel]               ← free tier, URL changes on restart
        ↓
[localhost:8000]             ← go-omnivore-rag, running locally in WSL
        ↓
[Azure OpenAI + Azure AI Search]
```

---

## Adapter Entry Point

The Fly.io app runs `cmd/server/main.go` (not the full RAG backend):

```
cmd/server/main.go
  └── reads  RAG_BACKEND_URL (Fly secret)
  └── wires  adapter.NewAdapterClient(ragBackendURL)
  └── starts api.NewChatHandler(client)  on PORT=8080
```

**Why a separate entry point:**  `cmd/main.go` calls `config.Load()` which calls
`requireEnv()` for all Azure credentials — the adapter has no Azure deps and would
crash immediately on Fly if it tried to start the full backend.

**Dockerfile:**  `fly deploy` now builds from `Dockerfile.adapter` (set in `fly.toml`).
The adapter image is ~15 MB (Alpine + single static Go binary) vs the full backend image.

---

## First-deploy checklist

- [ ] Confirm `RAG_BACKEND_URL` secret is set: `fly secrets list`
- [ ] Run `fly deploy` (builds `Dockerfile.adapter`, deploys adapter binary)
- [ ] Verify adapter: `curl https://ask-banner.fly.dev/health` → `{"status":"ok"}`
- [ ] Set `RAG_ADAPTER_URL=https://ask-banner.fly.dev` in Botpress Cloud → Configuration → Environment Variables
- [ ] Re-publish bot in Botpress Studio (env vars only take effect on publish)
- [ ] Wire Botpress Execute Code nodes — see [BOTPRESS-SETUP.md](BOTPRESS-SETUP.md)
- [ ] End-to-end test: open `demo/index.html`, type a question, verify answer appears

## Related docs

| Doc | Content |
|---|---|
| [LOCAL-DEV.md](LOCAL-DEV.md) | Full local setup, all env vars, common commands |
| [BOTPRESS-SETUP.md](BOTPRESS-SETUP.md) | Execute Code snippets, flow design, widget config |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | Symptoms → root cause → fix for all layers |
| [CHATBOT.md](CHATBOT.md) | Architecture, TDD phases, stretch goals |
