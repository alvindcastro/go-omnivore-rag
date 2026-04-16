# Botpress Cloud Setup Guide

How to configure the Botpress Cloud bot so it talks to the Ask Banner adapter.

---

## What Botpress does in this stack

Botpress Cloud hosts the conversation flow and serves the webchat widget embedded in `demo/index.html`. It calls the adapter (`ask-banner.fly.dev`) via HTTP from Execute Code nodes. It never talks to go-omnivore-rag directly.

```
[User types in widget]
        ↓
[Botpress Flow]
    ├── Execute Code → POST /chat/sentiment   (pre-filter)
    ├── Execute Code → POST /chat/intent      (classify)
    └── Execute Code → POST /chat/ask         (answer)
        ↓
[ask-banner.fly.dev]    ← all three calls go here
```

---

## Account and bot info

| Field | Value |
|---|---|
| Workspace ID | `wkspace_01KPA7D6WYGBEWGQ2MTCCPTFFA` |
| Bot ID | `3b6cf557-bc0a-4197-b16a-29c79706809f` |
| Bot dashboard | Botpress Cloud → Workspaces → your workspace → Bots |

---

## Step 1 — Set the environment variable

In Botpress Cloud → your bot → **Configuration** → **Environment Variables**:

```
Key:   RAG_ADAPTER_URL
Value: https://ask-banner.fly.dev
```

> **Important:** Re-publish the bot after saving. Environment variables are only picked up at publish time.

For local adapter dev, temporarily swap the value to your ngrok URL for the adapter.

---

## Step 2 — Flow design overview

```
[Start]
    │
    ▼
[Capture message]         ← event.preview contains the user's text
    │
    ▼
[Execute Code: Sentiment] ─── score > 0.7? ──► [Escalation message] ──► [End]
    │ score ≤ 0.7
    ▼
[Execute Code: Intent]
    │
    ▼
[Execute Code: Ask]       ─── escalate = true? ──► [Escalation message] ──► [End]
    │ escalate = false
    ▼
[Display answer + sources]
    │
    ▼
[👍 / 👎 quick replies]  (optional)
    │
    ▼
[End / loop back to Start]
```

---

## Step 3 — Execute Code snippets

Paste each snippet into its Execute Code node in Botpress Studio.

### Node 1 — Sentiment pre-filter

```javascript
const axios = require('axios');
const RAG = process.env.RAG_ADAPTER_URL;

try {
  const r = await axios.post(
    `${RAG}/chat/sentiment`,
    { message: event.payload.text },
    { timeout: 10000 }
  );
  workflow.sentimentScore = r.data.score;
  workflow.sentiment = r.data.sentiment; // "Positive" | "Neutral" | "Frustrated"
} catch (err) {
  // On error, treat as Neutral so the flow continues
  workflow.sentimentScore = 0;
  workflow.sentiment = 'Neutral';
  console.error('sentiment error:', err.message);
}
```

**What to do with the result:**
Add a Condition node after this:
- `workflow.sentimentScore > 0.7` → send escalation message, end
- otherwise → continue to intent node

---

### Node 2 — Intent detection

```javascript
const axios = require('axios');
const RAG = process.env.RAG_ADAPTER_URL;

try {
  const r = await axios.post(
    `${RAG}/chat/intent`,
    { message: event.payload.text },
    { timeout: 10000 }
  );
  workflow.intent = r.data.intent;             // e.g. "BannerRelease"
  workflow.intentConfidence = r.data.confidence;
} catch (err) {
  workflow.intent = 'General';
  workflow.intentConfidence = 0;
  console.error('intent error:', err.message);
}
```

**Possible intent values (internal users):**

| Value | Routes to | Example questions |
|---|---|---|
| `BannerRelease` | `/banner/ask` `module_filter=General` | "what changed in 9.3.37", "breaking changes", "upgrade notes" |
| `BannerFinance` | `/banner/ask` `module_filter=Finance` | "GL posting rules", "AR configuration", "budget setup" |
| `SopQuery` | `/sop/ask` | "steps for smoke test", "procedure for job submission" |
| `BannerAdmin` | `/banner/ask` `module_filter=General` | "how to configure Banner admin pages", "module setup" |
| `BannerUsage` | `/banner/general/ask` `source_type=banner_user_guide` | "what is Banner Access Management", "how do I navigate Banner", "what is FGAJVCD in Banner" |
| `General` | `/banner/ask` `module_filter=General` | everything else |

> **Note:** The `BannerRelease` intent routes to `/banner/ask` in the adapter. For a full structured summary (breaking changes, action items), add a separate Execute Code node calling `/banner/summarize/full` — see [Stretch: Release Summary node](#stretch-release-summary-node) below.

---

### Node 3 — Main ask

```javascript
const axios = require('axios');
const RAG = process.env.RAG_ADAPTER_URL;

try {
  const r = await axios.post(
    `${RAG}/chat/ask`,
    {
      message:    event.payload.text,
      session_id: event.botId + '-' + event.userId,
      intent:     workflow.intent || 'General',
    },
    { timeout: 25000 }  // adapter has 30 s backend timeout; keep this under that
  );

  workflow.answer     = r.data.answer;
  workflow.confidence = r.data.confidence;
  workflow.escalate   = r.data.escalate;
  workflow.sources    = r.data.sources;   // array of {title, page, source_type}
} catch (err) {
  workflow.answer    = 'Something went wrong. Please try again.';
  workflow.escalate  = true;
  workflow.sources   = [];
  console.error('ask error:', err.message, err.response?.status);
}
```

**What to do with the result:**
Add a Condition node after this:
- `workflow.escalate === true` → escalation message
- otherwise → display answer

---

### Displaying the answer

In the Text node that shows the answer, use:

```
{{workflow.answer}}
```

To show source citations (optional):

```javascript
// In a Send Message node or another Execute Code node
let citations = '';
if (workflow.sources && workflow.sources.length > 0) {
  const top = workflow.sources.slice(0, 2);
  citations = top.map(s => `• ${s.title} (p. ${s.page})`).join('\n');
}
workflow.citations = citations || 'No sources';
```

Then in a Text node:
```
{{workflow.answer}}

Sources:
{{workflow.citations}}
```

---

### Escalation message

Use a Text node with something like:

```
I wasn't able to find a confident answer for that.

Please contact the Banner Admin team:
📧 banner-admin@example.edu
☎️ ext. 1234
🕐 Mon–Fri, 8 AM–5 PM
```

Or for the frustrated sentiment path:

```
I can see you're having a frustrating experience. Let me connect you with someone who can help directly.

📧 banner-admin@example.edu
☎️ ext. 1234
```

---

## Step 4 — Publish

After saving flow changes, always hit **Publish** (top-right button in Botpress Studio). The webchat widget always serves the last published version. Unpublished changes are invisible to the widget.

---

## Stretch: Release Summary node

The `BannerRelease` intent could trigger a richer response using `/banner/summarize/full`. This requires knowing the Banner module and version, so you need a slot-filling step first.

```javascript
// Only run this when workflow.intent === 'BannerRelease'
// Assumes workflow.bannerVersion was collected by a prior slot-fill step

const axios = require('axios');
const RAG_BACKEND = process.env.RAG_ADAPTER_URL; // Note: this calls adapter's /chat/ask
// Or if you add a /chat/summarize endpoint to the adapter, call that instead

// For now, the adapter routes BannerRelease → /banner/ask (general)
// A dedicated summarize flow would need:
//   POST {adapter}/chat/summarize
//   body: { filename, banner_module, banner_version }
// That endpoint doesn't exist yet — see CHATBOT.md stretch goals.
```

---

## Webchat widget reference

The embed code in `demo/index.html` (already set up):

```html
<script src="https://cdn.botpress.cloud/webchat/v3.6/inject.js"></script>
<script src="https://files.bpcontent.cloud/2026/04/16/04/20260416040523-XQHQD1JB.js" defer></script>
```

**Nice to know:**
- Botpress is now on v3.6. The old `window.botpressWebChat.init()` (v2) and `window.botpress.init()` (v2.2) APIs no longer work.
- The second script is a bot-specific `config.js` generated by Botpress — it contains all widget configuration (botId, name, theme, etc.). Get it fresh from Studio → **Share** → embed snippet.
- The widget only shows the **last published** version of the bot. Always hit **Publish** after flow changes.
- The widget is loaded from Botpress CDN — no self-hosting required.
- To re-generate the config.js URL (e.g. after bot settings change): Studio → Share → copy the two script tags again.

---

## Testing the bot

### Option 1 — Botpress Studio Preview (fastest, tests unpublished changes)

Open the flow in Studio and click the **Preview** button (lightning bolt ▶ icon, top-right):

```
https://studio.botpress.cloud/3b6cf557-bc0a-4197-b16a-29c79706809f/flows/wf-main
```

The Preview panel opens on the right. Type a message and watch Execute Code nodes fire in the Logs tab.
**This tests the current draft — no publish required.**

### Option 2 — Shareable webchat URL (tests the last published version)

Get the current shareable URL from Studio → **Share** → copy the link. It looks like:
```
https://cdn.botpress.cloud/webchat/v3.6/shareable.html?botId=3b6cf557-bc0a-4197-b16a-29c79706809f
```

### Option 3 — demo/index.html (tests last published version, embedded widget)

Open `demo/index.html` in a browser (double-click or `open demo/index.html`). The Botpress chat bubble appears bottom-right. Click it to start a conversation.

> **If the chat bubble does not appear:** check the browser console for errors. The embed code uses two `<script>` tags — `inject.js` (v3.6) and a bot-specific `config.js` from `files.bpcontent.cloud`. If either fails to load, get fresh embed code from Studio → **Share**.

### Option 4 — curl the adapter directly (bypass Botpress entirely)

```bash
# adapter running locally on :8080
curl -s -X POST http://localhost:8080/chat/ask \
  -H 'Content-Type: application/json' \
  -d '{"message":"what changed in Banner 9.3.37?","session_id":"test-1"}' | jq .
```

---

## Nice to knows

**Botpress vs. adapter responsibility split:**
- Botpress handles: conversation state, user turns, slot filling, branching, the widget UI.
- The adapter handles: intent classification, sentiment analysis, RAG routing, confidence scoring.
- Never put RAG logic in Botpress Execute Code — keep it all in the adapter.

**Re-publish cadence:**
- Flow changes → must publish.
- Environment variable changes → must publish.
- Adapter code changes (Fly deploy) → no Botpress publish needed.

**Botpress free plan limits:**
- Botpress Cloud free tier includes a generous message quota for demos.
- If you hit limits, the bot returns a quota-exceeded error. Check Botpress billing dashboard.

**Logs in Botpress Studio:**
- Studio → Logs tab → filter by bot ID.
- Execute Code `console.error()` calls appear here.
- Use this to debug Execute Code failures without needing to watch Fly logs.

**`event.preview` vs `event.payload.text`:**
- `event.preview` is a display-safe truncated string (safe for logging, may be truncated at ~100 chars).
- `event.payload.text` is the full raw message text.
- Use `event.payload.text` for the actual intent/sentiment/ask calls to avoid truncation on long messages.
