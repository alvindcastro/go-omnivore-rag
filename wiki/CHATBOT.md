# Ask Banner — Chatbot Reference

Botpress adapter over `go-omnivore-rag`. Internal-user Q&A for Banner ERP: release notes, SOPs, and user guide navigation.

See [BOTPRESS-SETUP.md](BOTPRESS-SETUP.md) for flow configuration and testing. See [CLAUDE.md](../CLAUDE.md) for routing rules and non-negotiables.

---

## Architecture

```
[Botpress Cloud Widget]
        ↓  (user types message)
[Botpress Flow — Execute Code nodes]
        ↓  axios HTTP calls
[Botpress Adapter — ask-banner.fly.dev]
        ├── POST /chat/ask        → routes to backend based on intent/source
        ├── POST /chat/sentiment  → rule-based frustration pre-filter
        └── POST /chat/intent     → keyword classifier (6 intents)
                ↓
[go-omnivore-rag backend — unchanged]
        ├── /banner/ask           (module_filter=General|Finance)
        ├── /banner/general/ask   (source_type=banner_user_guide)
        ├── /banner/student/ask   (source_type=banner_user_guide)
        ├── /banner/finance/ask   (source_type=banner_user_guide)
        ├── /sop/ask
        └── /banner/summarize/full
                ↓
[Azure OpenAI GPT-4o-mini + Azure AI Search]
```

---

## Backend API surface

### Banner endpoints

| Method | Path | Purpose | Key fields |
|---|---|---|---|
| `POST` | `/banner/ask` | Q&A over Banner release notes | `question` (required), `module_filter`, `version_filter`, `year_filter`, `top_k` |
| `POST` | `/banner/general/ask` | Q&A over general user guide PDFs | `question` (required), `source_type=banner_user_guide`, `top_k` |
| `POST` | `/banner/student/ask` | Q&A over student user guide PDFs | same |
| `POST` | `/banner/finance/ask` | Q&A over finance user guide PDFs | same |
| `POST` | `/banner/summarize/full` | Full structured summary of a release | `filename` (required), `banner_module`, `banner_version`, `top_k` |

### SOP endpoints

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/sop/ask` | Q&A over SOP documents |
| `GET` | `/sop` | List all ingested SOPs |

### Key response shapes

**`rag.AskResponse`** — returned by all `/ask` endpoints:
```json
{
  "answer": "string",
  "question": "string",
  "retrieval_count": 3,
  "sources": [
    {
      "banner_module": "General",
      "banner_version": "9.3.37",
      "document_title": "string",
      "filename": "string",
      "page": 12,
      "score": 0.033,
      "sop_number": "string",
      "source_type": "banner|sop|banner_user_guide",
      "year": "2024"
    }
  ]
}
```

`sources[0].score` is the raw Azure AI Search hybrid score — **not** a normalized 0–1 value. Valid answers typically score 0.01–0.05. See [RUNBOOK.md](RUNBOOK.md) § Score Distribution.

**`rag.FullSummaryResponse`** — returned by `/banner/summarize/full`:
```json
{
  "action_items": "string",
  "banner_module": "string",
  "banner_version": "string",
  "breaking_changes": "string",
  "chunks_analyzed": 14,
  "compatibility": "string",
  "filename": "string",
  "source_pages": [1, 4, 7],
  "what_changed": "string"
}
```

---

## Adapter response contract

What the adapter returns to Botpress (from `POST /chat/ask`):

```json
{
  "answer": "string",
  "confidence": 0.033,
  "sources": [{ "title": "string", "page": 12, "source_type": "banner" }],
  "escalate": false
}
```

| Backend field | → | Adapter field | Notes |
|---|---|---|---|
| `sources[0].score` | → | `confidence` | 0.0 if no sources |
| `retrieval_count == 0` OR `confidence < floor` | → | `escalate = true` | See RUNBOOK for floor value |
| `document_title` | → | `sources[i].title` | rename only |

---

## User Guide Q&A routing

User guide sources route to indexed Ellucian Banner user guide PDFs (`source_type=banner_user_guide`), not release notes.

| Question type | Source | Backend |
|---|---|---|
| "What changed in Banner 9.3.37?" | `banner` | `/banner/ask` (release notes) |
| "How do I enter a journal entry?" | `user_guide_finance` | `/banner/finance/ask` |
| "How to restart the Banner server" | `sop` | `/sop/ask` |
| "Where is the student name search?" | `user_guide_student` | `/banner/student/ask` |
| "How do I navigate the Banner main menu?" | `user_guide` | `/banner/general/ask` |

**Never set `version_filter` or `year_filter` for user guide sources** — user guide PDFs carry no version metadata; filtering returns 0 results.

---

## Stretch goals

| Feature | Notes |
|---|---|
| `/banner/summarize/full` as dedicated flow path (slot: module + version) | Needs `/chat/summarize` adapter endpoint |
| Multi-turn: pass `conversation_history` to `/chat/ask` | Conversational state management |
| HuggingFace zero-shot fallback when keyword score < 0.3 | ML model integration |
| Confidence logging → Streamlit drift dashboard | Monitor, evaluate, retrain |
| Real `/sop` list endpoint powering a "Browse SOPs" flow node | Full API surface integration |
