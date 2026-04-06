# Integration Brainstorm: LangGraph + n8n

Ideas for wiring go-omnivore-rag into LangGraph agent workflows and n8n automation pipelines.
This is a brainstorm — not all ideas are equal or immediately practical.

---

## Table of Contents

1. [Prerequisites Before Either Integration](#prerequisites-before-either-integration)
2. [LangGraph — What It Buys You](#langgraph--what-it-buys-you)
3. [LangGraph Ideas](#langgraph-ideas)
4. [n8n — What It Buys You](#n8n--what-it-buys-you)
5. [n8n Ideas](#n8n-ideas)
6. [Hybrid: LangGraph + n8n Together](#hybrid-langgraph--n8n-together)
7. [Implementation Priority](#implementation-priority)
8. [Things That Need to Be Built First](#things-that-need-to-be-built-first)

---

## Prerequisites Before Either Integration

Both tools will call the go-omnivore-rag API over HTTP. Two things need to exist first:

### 1. API Key Authentication

Right now any HTTP client can call any endpoint including `/index/create` (destructive) and
`/banner/ingest` (slow/expensive). Before exposing the API to external orchestrators:

```go
// internal/api/router.go
r.Use(apiKeyMiddleware(cfg.APIKey))
```

Add `API_KEY` to `.env` and `config.go`. Every LangGraph tool call and n8n HTTP Request node
should send `Authorization: Bearer <key>`.

### 2. CORS (for browser-based n8n)

If n8n is running in a browser context (cloud-hosted n8n.io), requests to `localhost:8000` will be
blocked. Either:
- Run n8n self-hosted on the same network as go-omnivore-rag
- Add `github.com/gin-contrib/cors` middleware to the Gin router

### 3. A Stable Base URL

Replace `localhost:8000` with a network-accessible address. Docker with an internal network or a
simple reverse proxy (Caddy/nginx) works. Both LangGraph and n8n need to reach it.

---

## LangGraph — What It Buys You

The current RAG pipeline is a straight line: embed → search → prompt → chat. LangGraph lets you
turn that into a **graph** with branches, loops, and multiple agents.

**When to reach for LangGraph:**
- Questions that require multiple RAG calls before answering
- Workflows where the agent should decide *which* endpoints to call
- Conversation memory across multiple turns
- Self-correction: re-ask with a refined query if the first answer is weak
- Human approval steps before expensive operations

**When not to bother:**
- Single-turn Q&A — the existing `/banner/ask` is already good enough
- Automation triggered by events — that's n8n's job

---

## LangGraph Ideas

### 1. Tool-Calling ReAct Agent (Starting Point)

The simplest integration. Wrap each API endpoint as a Python tool and hand them to a ReAct agent.
The agent reasons about which tools to call and in what order.

```python
from langgraph.prebuilt import create_react_agent

tools = [
    banner_ask_tool,       # POST /banner/ask
    sop_ask_tool,          # POST /sop/ask
    banner_summarize_tool, # POST /banner/summarize/full
    list_sops_tool,        # GET /sop
]

agent = create_react_agent(model, tools)
result = agent.invoke({"messages": [("user", "What changed in Banner General 9.3.37.2 and do any of the action items affect the SOPs?")]})
```

**What the agent does:** It independently calls `/banner/summarize/changes`, then `/sop/ask` to
check relevance, and synthesizes a cross-document answer. The current system can't do this — it
requires two separate manual HTTP calls and human synthesis.

**Tool schema example:**

```python
@tool
def banner_ask(question: str, version_filter: str = "", module_filter: str = "", top_k: int = 5) -> dict:
    """Query Banner ERP release notes with a natural language question.
    Use version_filter and module_filter to scope the search."""
    return requests.post(f"{BASE_URL}/banner/ask", json={
        "question": question, "version_filter": version_filter,
        "module_filter": module_filter, "top_k": top_k
    }, headers={"Authorization": f"Bearer {API_KEY}"}).json()
```

---

### 2. Router Agent (Banner vs. SOP Classification)

Add a routing step before the RAG call. The router classifies the question and sends it to the
right endpoint, instead of the user having to know which endpoint to use.

```
User question
     ↓
  Router Node
  (classify: is this about a Banner release, an SOP procedure, or both?)
     ↓            ↓           ↓
Banner Ask     SOP Ask    Call Both
     ↓            ↓           ↓
          Answer Assembly
               ↓
           Final Answer
```

**State schema:**

```python
class RAGState(TypedDict):
    question: str
    route: Literal["banner", "sop", "both"]
    banner_result: dict
    sop_result: dict
    final_answer: str
```

**Why:** Users shouldn't need to know the difference between Banner and SOP. A single entry point
routes intelligently.

---

### 3. Multi-Agent Supervisor

Two specialist agents coordinated by a supervisor. Each specialist only knows its domain.

```
              Supervisor
             /           \
    Banner Expert      SOP Expert
    (/banner/*)        (/sop/*)
```

```python
from langgraph_supervisor import create_supervisor

banner_agent = create_react_agent(model, [banner_ask_tool, banner_summarize_tool],
                                   system_prompt="You are an expert in Banner ERP release notes...")
sop_agent = create_react_agent(model, [sop_ask_tool, list_sops_tool],
                                system_prompt="You are an expert in Standard Operating Procedures...")

supervisor = create_supervisor(model, [banner_agent, sop_agent],
    prompt="Route questions to the right expert. For questions spanning both, query both.")
```

**Use case:** A user asks "We're upgrading to Banner 9.3.37.2 — what procedures do we need to run?"
The supervisor delegates the Banner part to Banner Expert and the procedures part to SOP Expert,
then combines.

---

### 4. Self-Reflection / Query Refinement Loop

The agent evaluates its own answer quality and retries with a refined query if the answer is weak.

```
Question → RAG call → Evaluate answer quality
                            ↓              ↓
                      Good answer    Weak/empty answer
                            ↓              ↓
                       Return it     Refine query → RAG call again (max 3 iterations)
```

**Why it helps:** The current system returns whatever GPT-4o-mini produces, even if the retrieved
chunks were poor matches. This loop catches "I don't have enough information" answers and tries
broadening the query (e.g., dropping version filter, increasing top_k).

**Evaluation heuristic (simple):**

```python
def evaluate_answer(answer: str) -> bool:
    weak_signals = ["don't have", "no information", "not mentioned", "cannot find"]
    return not any(s in answer.lower() for s in weak_signals)
```

---

### 5. Upgrade Impact Analyzer Graph

A purpose-built graph for Banner upgrade planning. Runs multiple summarization calls in parallel,
then synthesizes a structured report.

```
        Input: version, module
              ↓
    ┌─────────┴─────────┐
 Breaking          Action Items    ← Parallel nodes (asyncio)
 Changes           Required
    └─────────┬─────────┘
    Compatibility Check
              ↓
     Risk Assessment Node
     (LLM: evaluate impact, flag blockers)
              ↓
     Structured Report Output
```

**Parallel execution in LangGraph:**

```python
from langgraph.graph import StateGraph, START, END

builder = StateGraph(UpgradeState)
builder.add_node("breaking", fetch_breaking_changes)
builder.add_node("actions", fetch_action_items)
builder.add_node("compatibility", fetch_compatibility)
builder.add_node("synthesize", synthesize_report)

builder.add_edge(START, "breaking")
builder.add_edge(START, "actions")        # Runs in parallel with breaking
builder.add_edge(START, "compatibility")  # Runs in parallel
builder.add_edge("breaking", "synthesize")
builder.add_edge("actions", "synthesize")
builder.add_edge("compatibility", "synthesize")
```

**Output:** A structured markdown report with Breaking Changes / Action Items / Compatibility
sections, plus a risk score and a "must do before upgrade" checklist.

---

### 6. Conversational Memory Agent

Wrap the RAG system with LangGraph's message history so users can ask follow-up questions.

```
Turn 1: "What changed in Banner General 9.3.37.2?"
Turn 2: "Which of those changes require DBA involvement?"
Turn 3: "Is there an SOP for the steps the DBA needs to take?"
```

Without memory, turn 2 and 3 have no context. With LangGraph's `MemorySaver` checkpointer, each
turn builds on the previous conversation.

```python
from langgraph.checkpoint.memory import MemorySaver

checkpointer = MemorySaver()
agent = create_react_agent(model, tools, checkpointer=checkpointer)

# Turn 1
agent.invoke({"messages": [("user", "What changed in Banner General 9.3.37.2?")]},
             config={"configurable": {"thread_id": "user-session-1"}})

# Turn 2 — agent remembers the previous answer
agent.invoke({"messages": [("user", "Which of those changes require DBA involvement?")]},
             config={"configurable": {"thread_id": "user-session-1"}})
```

**Practical use:** A web chat UI where IT staff have multi-turn conversations about a specific
upgrade, building context as they go.

---

### 7. Human-in-the-Loop Approval for Destructive Operations

Before triggering `/banner/ingest` or `/index/create`, pause the graph and ask for human approval.

```
Agent decides to re-ingest documents
         ↓
    interrupt()   ← Graph pauses, sends approval request (Teams/Slack)
         ↓
  Human approves
         ↓
   graph.invoke(Command(resume=True))
         ↓
    POST /banner/ingest
```

```python
from langgraph.types import interrupt

def ingest_node(state):
    approval = interrupt({
        "question": f"Approve re-ingestion of {state['docs_path']}?",
        "details": f"This will re-embed all documents. Cost: ~{estimated_cost} tokens."
    })
    if approval["approved"]:
        return call_ingest_api(state['docs_path'])
    return {"status": "cancelled"}
```

**Why:** Ingestion is slow, costs money, and a bug could trigger it in a loop. Human approval
gates make automated workflows safer.

---

### 8. MCP Server Wrapper

Wrap go-omnivore-rag as an MCP (Model Context Protocol) server so any MCP-compatible LLM client
(Claude Desktop, Cursor, LangGraph MCP client) can use it as a tool without custom integration code.

**Concept:**
- Add a thin Python MCP server that translates MCP tool calls → HTTP calls to go-omnivore-rag
- OR implement MCP protocol directly in Go (the protocol is simple JSON-RPC over stdin/SSE)

```python
# mcp_server.py
from mcp.server.fastmcp import FastMCP

mcp = FastMCP("omnivore-rag")

@mcp.tool()
def banner_ask(question: str, version: str = "") -> str:
    """Query Banner ERP release notes."""
    response = requests.post(f"{BASE_URL}/banner/ask", json={"question": question, "version_filter": version})
    return response.json()["answer"]
```

**What it unlocks:** Claude Desktop, Cursor, and any LangGraph agent with an MCP client can use
go-omnivore-rag as a native tool without writing integration code per client.

---

## n8n — What It Buys You

n8n is a visual workflow automation tool. Think Zapier/Make but self-hosted and with AI nodes built in.

**When to reach for n8n:**
- Triggering ingestion when documents are uploaded (SharePoint, OneDrive, Teams)
- Scheduled jobs (daily blob sync, weekly report generation)
- Connecting the RAG system to Slack / Teams / Email without writing code
- Non-technical users who need to interact with the system via a UI

**When not to bother:**
- Complex reasoning chains — n8n's AI Agent is capable but LangGraph is more powerful
- If everything is code anyway — just write a Go/Python script

---

## n8n Ideas

### 1. Tools Agent: Unified Q&A Node

The simplest useful integration. An n8n AI Agent with multiple HTTP Request Tool sub-nodes.
Users interact with one agent that decides whether to call `/banner/ask` or `/sop/ask`.

```
[Chat Trigger] or [Webhook]
        ↓
   [AI Agent node]
   ├── Tool: HTTP Request → POST /banner/ask
   ├── Tool: HTTP Request → POST /sop/ask
   ├── Tool: HTTP Request → GET /sop
   └── Tool: HTTP Request → POST /banner/summarize/full
        ↓
   [Respond to user] (Slack / Teams / webhook response)
```

**n8n HTTP Request Tool node config:**
- URL: `http://go-omnivore-rag:8000/banner/ask`
- Method: POST
- Send Headers: `Authorization: Bearer {{$credentials.omnivoreApiKey}}`
- Body (from AI): `{"question": "...", "top_k": 5}`
- Tool description: "Search Banner ERP release notes for a given question"

---

### 2. Document Upload → Auto Ingest

Trigger ingestion automatically when a new document is uploaded to SharePoint or dropped in a folder.

**SharePoint trigger:**
```
[SharePoint Trigger: file created in /Sites/IT/Banner Release Notes]
        ↓
[IF: file extension is .pdf]
        ↓ YES                        ↓ NO (if .docx)
[HTTP: POST /banner/blob/sync]   [HTTP: POST /sop/ingest]
        ↓                                  ↓
[Slack: notify #banner-upgrades]   [Slack: notify #sop-updates]
        ↓
[HTTP: GET /index/stats → append to message]
```

**Local folder watch (self-hosted n8n with filesystem access):**
```
[Cron: every 15 minutes]
        ↓
[Execute Command: check for new files in data/docs/]
        ↓ (if new files found)
[HTTP: POST /banner/ingest {"overwrite": false}]
        ↓
[HTTP: POST /sop/ingest {"overwrite": false}]
```

---

### 3. Daily Blob Sync + Health Report

```
[Cron: daily at 06:00]
        ↓
[HTTP: GET /health]  ──── fail ──→ [Slack: "⚠ go-omnivore-rag is down"]
        ↓ success
[HTTP: POST /banner/blob/sync]
        ↓
[HTTP: GET /index/stats]
        ↓
[Slack: "Daily sync complete. Index contains {{chunks}} chunks across {{docs}} documents."]
```

This is the highest-ROI n8n workflow to build first — pure automation, no LLM cost.

---

### 4. Slack Bot: Ask Banner

Users @mention a Slack bot and get a RAG answer in the thread.

```
[Slack Trigger: app_mention]
        ↓
[Extract message text] (strip @botname)
        ↓
[HTTP: POST /banner/ask {"question": "{{message_text}}", "top_k": 5}]
        ↓
[Format response: answer + source list]
        ↓
[Slack: reply in thread with formatted answer + sources]
```

**Response formatting (n8n Code node):**
```javascript
const result = $input.first().json;
const sources = result.sources.map((s, i) =>
  `[${i+1}] ${s.filename} (p.${s.page}) — ${s.banner_module} ${s.banner_version}`
).join('\n');
return { text: `${result.answer}\n\n*Sources:*\n${sources}` };
```

---

### 5. Microsoft Teams Bot

Same as the Slack bot but via Teams outgoing webhook.

```
[Webhook: Teams outgoing webhook POST]
        ↓
[IF: message starts with "!banner"]
        ↓ YES
[Strip prefix, call /banner/ask]
        ↓
[Format: Adaptive Card with answer + sources]
        ↓
[HTTP: POST back to Teams webhook URL]
```

**Adaptive Card** gives a much nicer response than plain text — collapsible sources, links to
filenames, version badges.

---

### 6. Weekly Upgrade Readiness Report

Every Monday, generate a structured upgrade impact report for all recent Banner releases and
email it to the IT team.

```
[Cron: Monday 08:00]
        ↓
[HTTP: POST /banner/summarize/full for each version in a list]
        ↓
[Code node: format as HTML email with sections per version]
        ↓
[Email: send to it-team@yourdomain.com]
        ↓
[OneDrive: save report as {date}-upgrade-readiness.html]
```

**n8n's loop node** can iterate over a list of versions `["9.3.37.2", "9.3.38.0"]` and call the
summarize endpoint for each.

---

### 7. Jira/ServiceNow Ticket Auto-Answer

When a new IT support ticket is created about a Banner upgrade question, automatically add a
RAG-generated answer as a comment.

```
[Webhook: Jira issue created]
        ↓
[IF: labels contain "banner-upgrade"]
        ↓
[HTTP: POST /banner/ask {"question": "{{issue.summary + description}}"}]
        ↓
[IF: answer confidence > 0.7 (check source count)]
        ↓ YES                             ↓ NO
[Jira: add comment with answer]    [Jira: add comment "No relevant docs found"]
        ↓
[Jira: set label "auto-answered"]
```

---

### 8. SOP Change Notification

When an SOP is re-ingested (updated), notify the relevant team.

```
[HTTP: POST /sop/ingest] (triggered by document upload)
        ↓
[HTTP: GET /sop] (get current SOP list with chunk counts)
        ↓
[Compare with previous run stored in n8n static data]
        ↓
[IF: new SOPs or chunk count changed]
        ↓
[Teams: notify #sop-updates with diff — which SOPs changed]
```

---

### 9. Index Health Monitor

Poll `/health` and `/index/stats` on a schedule. Alert if something looks wrong.

```
[Cron: every 30 minutes]
        ↓
[HTTP: GET /health]
        ↓
[HTTP: GET /index/stats]
        ↓
[IF: chunks < expected_minimum OR health != "ok"]
        ↓
[PagerDuty / Slack: fire alert with details]
```

---

### 10. n8n Form: Self-Service Q&A Portal

A simple web form (n8n's Form trigger) that non-technical staff can use without needing to
know about REST APIs.

```
[n8n Form Trigger]
Fields:
  - Question (text)
  - Document type (dropdown: Banner | SOP | Both)
  - Version filter (text, optional)
        ↓
[Switch: route by document type]
        ↓
[HTTP: call appropriate /ask endpoint(s)]
        ↓
[n8n Form Response: display formatted answer]
```

n8n Form triggers create a hosted HTML form with no additional frontend work.

---

## Hybrid: LangGraph + n8n Together

The two tools complement each other. n8n handles scheduling and integrations; LangGraph handles
complex reasoning.

### Pattern A: n8n Triggers → LangGraph Does the Thinking

Deploy the LangGraph agent as a FastAPI endpoint. n8n calls it for anything that requires
multi-step reasoning.

```
n8n: SharePoint document uploaded
     ↓
n8n: POST /ingest to go-omnivore-rag
     ↓
n8n: POST http://langgraph-service/analyze
     {"action": "summarize_and_notify", "doc": "Banner_General_9.3.38.pdf"}
     ↓
LangGraph: runs upgrade impact analysis graph (parallel summarize calls)
     ↓
LangGraph: returns structured report
     ↓
n8n: sends Teams card + archives to OneDrive
```

### Pattern B: LangGraph → n8n for Notifications

LangGraph completes reasoning → calls an n8n webhook to trigger business actions.

```
LangGraph: detects breaking change requires DBA action
     ↓
LangGraph: POST n8n webhook {"action": "alert_dba", "details": "..."}
     ↓
n8n: formats Teams/Slack message, creates Jira ticket, sends email
```

LangGraph stays focused on reasoning; n8n handles all the integrations.

### Pattern C: n8n as LangGraph Orchestrator

n8n manages the schedule and input, calls LangGraph agents as HTTP tools, handles output routing.

```
n8n: Cron trigger
     ↓
n8n: HTTP Request → LangGraph multi-agent endpoint
     ↓
n8n: Parse result
     ↓
n8n: Branch based on result → Slack / Jira / email / archive
```

---

## Implementation Priority

**Highest ROI, lowest effort:**

1. **n8n: Daily blob sync + Slack notification** — pure automation, no LLM, demonstrates value immediately
2. **n8n: Tools Agent for Slack/Teams Q&A** — non-technical staff can use it right away
3. **LangGraph: ReAct agent with all endpoints as tools** — foundation for everything else

**Medium effort, high value:**

4. **n8n: SharePoint upload → auto ingest** — eliminates manual ingestion step
5. **LangGraph: Router agent (Banner vs. SOP classifier)** — better UX than two separate endpoints
6. **LangGraph: Upgrade Impact Analyzer graph** — structured parallel report generation

**Longer term:**

7. **LangGraph: Conversational memory agent** — requires a session management layer
8. **LangGraph: Human-in-the-loop approval** — requires n8n or a UI to receive/send approvals
9. **MCP server wrapper** — enables Claude Desktop, Cursor, and other MCP clients

---

## Things That Need to Be Built First

Before any of the above works reliably:

| What | Why Needed | Where | Status |
|------|-----------|-------|--------|
| API key auth middleware | Every external caller needs auth | `internal/api/router.go` | ⬜ TODO |
| CORS middleware | n8n cloud / browser clients need it | `internal/api/router.go` | ⬜ TODO |
| Request ID header | Correlate n8n workflow logs with go-omnivore-rag logs | `internal/api/router.go` | ⬜ TODO |
| Docker / stable network address | Both tools need a reachable URL (not localhost) | `Dockerfile` + `docker-compose.yml` | ✅ Done |
| Structured JSON logging | Machine-parseable logs for observability | Replace `log.Printf` with `slog` | ⬜ TODO |
| Confidence score in response | n8n conditions and LangGraph evaluation need it | Add `score` field to `AskResponse` | ⬜ TODO |

Auth middleware is the remaining blocker. Everything else is nice-to-have before production use.
