# Claude Agents for go-omnivore-rag

Brainstorm of Claude agent designs that use go-omnivore-rag as the backend knowledge system.
Each agent wraps the existing HTTP API as tools — no changes to the Go backend required.

---

## Table of Contents

1. [Overview & Prerequisites](#overview--prerequisites)
2. [Agent 1: Banner Ask Agent](#agent-1-banner-ask-agent)
3. [Agent 2: SOP Navigator Agent](#agent-2-sop-navigator-agent)
4. [Agent 3: Upgrade Impact Analyzer Agent](#agent-3-upgrade-impact-analyzer-agent)
5. [Agent 4: Conversational RAG Agent](#agent-4-conversational-rag-agent)
6. [Agent 5: Document Ingestion Orchestrator Agent](#agent-5-document-ingestion-orchestrator-agent)
7. [Agent 6: SOP Gap Analyzer Agent](#agent-6-sop-gap-analyzer-agent)
8. [Agent 7: Index Health & Diagnostics Agent](#agent-7-index-health--diagnostics-agent)
9. [Agent 8: Internal Banner Chatbot Agent](#agent-8-internal-banner-chatbot-agent)
10. [Agent 9: Confidence Calibration Agent](#agent-9-confidence-calibration-agent)
11. [Tool Reference: API-to-Tool Mapping](#tool-reference-api-to-tool-mapping)
12. [Implementation Notes](#implementation-notes)
13. [Priority Recommendation](#priority-recommendation)

---

## Overview & Prerequisites

All agents below use the [Anthropic Python SDK](https://github.com/anthropics/anthropic-sdk-python)
with tool use. Each tool is a thin wrapper around a go-omnivore-rag HTTP endpoint.

**Runtime requirements:**
- go-omnivore-rag running (Docker or local) at a known base URL
- `ANTHROPIC_API_KEY` set in environment
- `OMNIVORE_BASE_URL` pointing to the running backend (e.g. `http://localhost:8000`)
- `OMNIVORE_API_KEY` if API key auth is enabled on the backend

**Recommended model:** `claude-opus-4-6` for multi-step reasoning agents; `claude-haiku-4-5` for
simple single-turn Q&A to minimize cost.

**Shared tool executor pattern:**

```python
import anthropic
import requests
import json
import os

BASE_URL = os.environ["OMNIVORE_BASE_URL"]
API_KEY = os.environ.get("OMNIVORE_API_KEY", "")

HEADERS = {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}

client = anthropic.Anthropic()

def call_api(method: str, path: str, body: dict | None = None) -> dict:
    url = f"{BASE_URL}{path}"
    if method == "GET":
        resp = requests.get(url, headers=HEADERS)
    else:
        resp = requests.post(url, json=body, headers=HEADERS)
    resp.raise_for_status()
    return resp.json()

def run_tool(tool_name: str, tool_input: dict) -> str:
    """Dispatch tool call to the correct API endpoint."""
    dispatch = {
        "banner_ask": lambda i: call_api("POST", "/banner/ask", i),
        "sop_ask": lambda i: call_api("POST", "/sop/ask", i),
        "sop_list": lambda i: call_api("GET", "/sop"),
        "banner_summarize": lambda i: call_api("POST", f"/banner/summarize/{i.pop('topic', 'full')}", i),
        "banner_ingest": lambda i: call_api("POST", "/banner/ingest", i),
        "sop_ingest": lambda i: call_api("POST", "/sop/ingest", i),
        "index_health": lambda i: call_api("GET", "/health"),
        "index_stats": lambda i: call_api("GET", "/index/stats"),
    }
    result = dispatch[tool_name](tool_input)
    return json.dumps(result)
```

---

## Agent 1: Banner Ask Agent

**Purpose:** Single-turn Q&A about Ellucian Banner ERP release notes. Routes to the right module
automatically. Returns a grounded answer with source citations.

**Use case:** IT staff paste a question into Slack/Teams/CLI and get an answer citing the exact
release note page.

**Type:** Simple tool-use agent (1–2 tool calls max).

### Tools

```python
BANNER_ASK_TOOLS = [
    {
        "name": "banner_ask",
        "description": (
            "Search Banner ERP release notes and answer a question using indexed documents. "
            "Use module_filter to scope by Banner module (Finance, Student, HR, General, etc.). "
            "Use version_filter to scope to a specific version like '9.3.37.2'. "
            "Returns answer text plus source chunks with page numbers and confidence scores."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string", "description": "The question to answer"},
                "module_filter": {"type": "string", "description": "Banner module name, e.g. Finance, Student, HR, General"},
                "version_filter": {"type": "string", "description": "Specific Banner version, e.g. 9.3.37.2"},
                "top_k": {"type": "integer", "description": "Number of source chunks to retrieve", "default": 5},
                "mode": {"type": "string", "enum": ["local", "web", "hybrid", "auto"], "default": "auto"},
            },
            "required": ["question"],
        },
    }
]
```

### System Prompt

```python
BANNER_ASK_SYSTEM = """You are a Banner ERP upgrade assistant for IT staff.
Answer questions about Ellucian Banner release notes using the banner_ask tool.

Rules:
- Always use the tool before answering. Never answer from prior knowledge alone.
- If the user mentions a module (Finance, Student, HR, General, Accounts Receivable, etc.), set module_filter.
- If the user mentions a version number, set version_filter.
- After tool call: quote the top_score from the response as your confidence level.
- If retrieval_count == 0, say "No relevant documents found" and suggest checking the index.
- Note: Azure AI Search scores for this index are typically 0.01–0.05 — a score of 0.033 is a valid answer, not low confidence.
- Format sources as: [Document Title, page N, module, version]
- Keep answers concise. Lead with the direct answer, then list sources."""
```

### Implementation

```python
def banner_ask_agent(question: str) -> str:
    messages = [{"role": "user", "content": question}]

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=1024,
            system=BANNER_ASK_SYSTEM,
            tools=BANNER_ASK_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")

        # Process tool calls
        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_tool(block.name, dict(block.input))
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example

```
User: What database changes are required for Banner Finance 9.3.22?

Agent: [calls banner_ask(question="database changes required", module_filter="Finance", version_filter="9.3.22")]

Answer: Banner Finance 9.3.22 requires the following database changes:
1. Run the FINUPGRADE.sql script before restarting application services
2. Three new index columns on FGBTRND table (see DBA checklist, page 4)
Confidence: 0.73

Sources:
- Banner Finance Release Notes 9.3.22, page 4 (Finance, 9.3.22)
- Banner Finance Release Notes 9.3.22, page 12 (Finance, 9.3.22)
```

---

## Agent 2: SOP Navigator Agent

**Purpose:** Browse and query Standard Operating Procedures. Can list available SOPs, answer
procedural questions, and walk through step-by-step procedures.

**Use case:** Functional analysts who need to follow a procedure without opening DOCX files.

**Type:** Multi-tool agent (list → ask flow).

### Tools

```python
SOP_TOOLS = [
    {
        "name": "sop_list",
        "description": "List all indexed Standard Operating Procedures (SOPs) with their IDs and titles.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "sop_ask",
        "description": (
            "Answer a question using indexed SOP documents. "
            "Use this after sop_list if the user wants a specific procedure, "
            "or directly if the question is clear."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string", "description": "The procedural question to answer"},
                "top_k": {"type": "integer", "default": 5},
            },
            "required": ["question"],
        },
    },
]
```

### System Prompt

```python
SOP_SYSTEM = """You are a procedure guide for IT operations staff.
You help users find and follow Standard Operating Procedures (SOPs).

Workflow:
1. If the user asks "what SOPs do you have?" or similar → call sop_list first.
2. For procedural questions → call sop_ask directly.
3. For step-by-step walkthroughs → call sop_ask, then present steps in numbered format.

Rules:
- Present procedures as numbered steps, not prose.
- Always cite the SOP number and section (e.g. "SOP122, Section 4.1").
- If retrieval_count == 0, warn the user no documents were found. Scores of 0.01–0.05 are normal for this index and do NOT indicate low quality.
- Never invent steps. If steps are missing from the retrieved context, say so."""
```

### Example

```
User: Walk me through the Banner upgrade smoke test

Agent: [calls sop_ask(question="Banner upgrade smoke test steps")]

Here are the steps from SOP122 - Smoke Test and Sanity Test Post Banner Upgrade:

**Pre-test:**
1. Verify all Banner application servers have restarted successfully
2. Confirm database connections are active

**Smoke Test:**
3. Log into Banner Admin Pages with test account
4. Navigate to each module listed in Appendix A
5. Confirm page loads without errors

[continues with retrieved steps...]

Source: SOP122, Section 3 — Smoke Test Procedure (confidence: 0.81)
```

---

## Agent 3: Upgrade Impact Analyzer Agent

**Purpose:** Produce a structured upgrade impact report for a specific Banner version. Runs
multiple summarization calls in parallel and synthesizes a single report with risk assessment.

**Use case:** IT manager preparing for a Banner upgrade needs a briefing document in minutes,
not hours of manual PDF reading.

**Type:** Multi-step parallel tool-call agent. Highest value, most complex.

### Tools

```python
UPGRADE_TOOLS = [
    {
        "name": "banner_summarize",
        "description": (
            "Summarize a specific aspect of Banner release changes. "
            "topic options: 'changes' (all changes), 'breaking' (breaking changes only), "
            "'actions' (required action items), 'compatibility' (compatibility notes), "
            "'full' (complete summary with all sections)."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "filename": {"type": "string", "description": "Banner release note filename to summarize"},
                "topic": {
                    "type": "string",
                    "enum": ["changes", "breaking", "actions", "compatibility", "full"],
                    "description": "Which aspect to summarize"
                },
                "banner_module": {"type": "string", "description": "e.g. Finance, Student, General"},
                "banner_version": {"type": "string", "description": "e.g. 9.3.37.2"},
                "top_k": {"type": "integer", "default": 10},
            },
            "required": ["filename", "topic"],
        },
    },
    {
        "name": "sop_ask",
        "description": "Check if existing SOPs cover a specific Banner change or procedure.",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "top_k": {"type": "integer", "default": 3},
            },
            "required": ["question"],
        },
    },
]
```

### System Prompt

```python
UPGRADE_ANALYZER_SYSTEM = """You are a Banner ERP upgrade impact analyzer.
When given a Banner version and module, produce a structured upgrade impact report.

Report structure:
1. **Breaking Changes** — anything that will break existing functionality
2. **Required Actions** — steps that MUST be completed (DBA, functional, sysadmin)
3. **Compatibility Notes** — dependencies, version requirements, integration impacts
4. **SOP Coverage** — which existing SOPs apply; flag gaps where no SOP exists
5. **Risk Assessment** — Overall risk level (Low/Medium/High) with justification

Process:
1. Call banner_summarize for topic='breaking', then 'actions', then 'compatibility' in sequence
   (or all in parallel if you can - prefer parallel calls)
2. Call sop_ask to check if SOPs exist for the major action items
3. Synthesize all results into the structured report above
4. Assign a risk level based on number of breaking changes and uncovered SOP gaps

Output format: Markdown with clear section headers."""
```

### Example Interaction

```
User: Analyze upgrade impact for Banner General 9.3.37.2

Agent: Let me pull all the relevant information in parallel...
[calls banner_summarize(topic="breaking", filename="Banner_General_Release_Notes_9.3.37.2.pdf", banner_version="9.3.37.2")]
[calls banner_summarize(topic="actions", filename="Banner_General_Release_Notes_9.3.37.2.pdf", banner_version="9.3.37.2")]
[calls banner_summarize(topic="compatibility", filename="Banner_General_Release_Notes_9.3.37.2.pdf", banner_version="9.3.37.2")]

[then calls sop_ask for each major action item found]

# Banner General 9.3.37.2 — Upgrade Impact Report

## Breaking Changes
- Login page authentication flow changed; custom SSO configs must be updated...

## Required Actions
- [ ] DBA: Run GENUPGRADE.sql before service restart
- [ ] Sysadmin: Update JBoss configuration (see page 7)
...

## SOP Coverage
- DBA upgrade script: **SOP154 applies** (verified)
- JBoss reconfiguration: **No SOP found** ⚠ — recommend creating one

## Risk Assessment: **MEDIUM**
2 breaking changes with clear remediation steps; 1 SOP gap identified.
```

---

## Agent 4: Conversational RAG Agent

**Purpose:** Multi-turn conversation about Banner and SOP content. Remembers context across
turns so users can ask follow-up questions naturally.

**Use case:** IT staff having an extended planning session about an upcoming upgrade.

**Type:** Stateful multi-turn agent with full tool access.

### Tools

Combines all tools: `banner_ask`, `sop_ask`, `sop_list`, `banner_summarize`.

### System Prompt

```python
CONVERSATIONAL_SYSTEM = """You are a Banner ERP knowledge assistant for IT staff.
You can answer questions about Banner release notes and Standard Operating Procedures.

You have access to:
- banner_ask: search Banner release notes
- sop_ask: search Standard Operating Procedures
- sop_list: list all available SOPs
- banner_summarize: get structured summaries of release changes

Behavior:
- Use conversation history to understand follow-up questions (e.g., "What about the Finance module?"
  refers to the version discussed earlier)
- When you retrieve information, state the source and confidence
- Maintain a mental model of what the user is trying to accomplish and proactively surface relevant info
- If a question spans both Banner and SOP domains, call both tools
- Escalate explicitly: say "I recommend verifying this with the source document" when retrieval_count == 0 (Azure scores of 0.01–0.05 are normal — do not escalate on score alone)"""
```

### Implementation

```python
def conversational_agent():
    """Interactive conversation loop."""
    messages = []
    print("Banner & SOP Assistant (type 'quit' to exit)\n")

    while True:
        user_input = input("You: ").strip()
        if user_input.lower() == "quit":
            break

        messages.append({"role": "user", "content": user_input})

        while True:
            response = client.messages.create(
                model="claude-sonnet-4-6",
                max_tokens=2048,
                system=CONVERSATIONAL_SYSTEM,
                tools=[*BANNER_ASK_TOOLS, *SOP_TOOLS, *UPGRADE_TOOLS],
                messages=messages,
            )

            if response.stop_reason == "end_turn":
                answer = next(b.text for b in response.content if b.type == "text")
                print(f"\nAssistant: {answer}\n")
                messages.append({"role": "assistant", "content": response.content})
                break

            # Process tool calls, append results, continue loop
            tool_results = []
            for block in response.content:
                if block.type == "tool_use":
                    result = run_tool(block.name, dict(block.input))
                    tool_results.append({
                        "type": "tool_result",
                        "tool_use_id": block.id,
                        "content": result,
                    })
            messages.append({"role": "assistant", "content": response.content})
            messages.append({"role": "user", "content": tool_results})
```

### Example Session

```
You: What changed in Banner General 9.3.37.2?
Assistant: [calls banner_summarize(topic="changes"...)]
           Here are the main changes in Banner General 9.3.37.2: [lists changes]

You: Which of those need DBA work?
Assistant: [understands "those" refers to 9.3.37.2 from context]
           [calls banner_ask(question="DBA database changes", version_filter="9.3.37.2")]
           The changes requiring DBA involvement are...

You: Is there an SOP for that?
Assistant: [calls sop_ask(question="database upgrade procedure DBA Banner")]
           Yes, SOP154 covers database-related upgrade steps. Section 3.2 specifically addresses...
```

---

## Agent 5: Document Ingestion Orchestrator Agent

**Purpose:** Safely orchestrate document ingestion with pre-flight checks and post-ingestion
validation. Prevents accidental duplicate ingestion and confirms index health afterward.

**Use case:** IT admin drops new Banner PDFs into `data/docs/banner/` and wants to verify
everything ingested cleanly.

**Type:** Sequential workflow agent with guard rails.

### Tools

```python
INGESTION_TOOLS = [
    {
        "name": "index_health",
        "description": "Check if the RAG backend is running and healthy.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "index_stats",
        "description": "Get current index statistics: document count, chunk count, index name.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "banner_ingest",
        "description": (
            "Trigger ingestion of Banner PDF documents from data/docs/banner/. "
            "WARNING: This is slow and costs Azure OpenAI tokens. "
            "Only call after confirming with the user."
        ),
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "sop_ingest",
        "description": "Trigger ingestion of SOP DOCX documents from data/docs/sop/.",
        "input_schema": {"type": "object", "properties": {}},
    },
]
```

### System Prompt

```python
INGESTION_SYSTEM = """You are an ingestion orchestrator for the go-omnivore-rag knowledge system.
Your job is to safely manage document ingestion with validation at each step.

Safe ingestion workflow:
1. Call index_health to confirm the backend is running
2. Call index_stats to record the BEFORE chunk count
3. Tell the user what you found and confirm they want to proceed with ingestion
4. If confirmed: call the appropriate ingest tool (banner_ingest or sop_ingest)
5. After ingestion: call index_stats again to verify chunk count increased
6. Report the delta: how many new chunks were added

Important guards:
- Never call ingest without first checking health
- Always report before/after chunk counts
- If health check fails, stop and report the error — don't attempt ingestion
- If chunk count does NOT increase after ingestion, flag this as a potential problem
- Ingestion is idempotent (deterministic chunk IDs) but still costs embedding tokens"""
```

### Example

```
User: Ingest the new Banner Finance release notes I just added

Agent: Let me check the system before we start...
[calls index_health → {"status": "ok", "model": "gpt-4o-mini"}]
[calls index_stats → {"chunks": 1247, "documents": 12, "index": "banner-upgrade-knowledge"}]

Current state:
- System: healthy
- Index: 1,247 chunks across 12 documents

Ready to ingest from data/docs/banner/. This will embed all PDFs and may cost Azure tokens
for any new documents. Shall I proceed? (yes/no)

User: yes

Agent: [calls banner_ingest]

Ingestion complete. Let me verify...
[calls index_stats → {"chunks": 1389, "documents": 14, "index": "banner-upgrade-knowledge"}]

Result:
- Added 142 new chunks
- Added 2 new documents
- Index now contains 1,389 chunks across 14 documents
```

---

## Agent 6: SOP Gap Analyzer Agent

**Purpose:** Cross-reference Banner release note action items against existing SOPs to identify
gaps — procedures that are required by a Banner upgrade but have no corresponding SOP.

**Use case:** Before a Banner upgrade, confirm that every required step has a documented procedure
that staff can follow.

**Type:** Multi-step analysis agent.

### Tools

Combines: `banner_summarize` (topic=actions/breaking) + `sop_ask` + `sop_list`.

### System Prompt

```python
GAP_ANALYZER_SYSTEM = """You are a documentation gap analyzer for Banner ERP upgrades.
Your job is to identify which Banner upgrade action items lack SOP coverage.

Process:
1. Call banner_summarize(topic="actions") to get all required action items for a release
2. Call banner_summarize(topic="breaking") to get breaking changes that may need new SOPs
3. Call sop_list to understand what SOPs currently exist
4. For each major action item, call sop_ask to check if a matching SOP exists
5. Compile a gap report

Output format:
## Action Items with SOP Coverage
- [action]: **Covered by SOP123** ✓

## Gaps — No SOP Found
- [action]: **No SOP found** ⚠
  Suggested SOP title: "Procedure — [action name]"
  Suggested sections: [outline the SOP structure based on the action item details]

## Recommendations
[Prioritized list of SOPs to create, with effort estimates]"""
```

### Example Output

```
## Banner General 9.3.37.2 — SOP Gap Analysis

## Action Items with SOP Coverage
- Post-upgrade smoke test: **Covered by SOP122** ✓
- Axiom service restart: **Covered by SOP154** ✓

## Gaps — No SOP Found
- JBoss session timeout reconfiguration: **No SOP found** ⚠
  Suggested SOP title: "Procedure — JBoss Session Timeout Update Post-Banner-Upgrade"
  Suggested sections:
  1. Prerequisites (JBoss admin access, Banner admin access)
  2. Backup current configuration
  3. Update timeout value in standalone.xml
  4. Restart JBoss service
  5. Verify Banner login session behavior

## Recommendations
1. Create JBoss configuration SOP (HIGH priority — blocking for upgrade)
2. Consider adding LDAP verification steps to SOP122
```

---

## Agent 7: Index Health & Diagnostics Agent

**Purpose:** Diagnose RAG system health issues. When retrieval quality degrades or the system
returns unexpected answers, this agent investigates and recommends fixes.

**Use case:** A user reports "the system isn't finding anything about Finance" — run this agent
to diagnose why.

**Type:** Diagnostic investigation agent.

### Tools

```python
DIAGNOSTIC_TOOLS = [
    {
        "name": "index_health",
        "description": "Check backend health and model configuration.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "index_stats",
        "description": "Get chunk counts and index metadata.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "banner_ask",
        "description": "Test a query against the index to check retrieval quality.",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "module_filter": {"type": "string"},
                "version_filter": {"type": "string"},
                "top_k": {"type": "integer", "default": 5},
                "mode": {"type": "string", "default": "local"},
            },
            "required": ["question"],
        },
    },
]
```

### System Prompt

```python
DIAGNOSTIC_SYSTEM = """You are a RAG system diagnostician. When given a complaint about
retrieval quality, investigate systematically and provide a root cause analysis.

Diagnostic checklist:
1. index_health — is the backend up? are model deployments configured?
2. index_stats — are there chunks in the index at all? expected range: 500–5000
3. Run a broad test query (no filters) — does it return results?
4. Run the same query with the user's filters — does filtering break it?
5. Check top_score: if < 0.010 on all results, likely a vector embedding mismatch

Common failure modes:
- Empty index (0 chunks): ingestion hasn't been run or failed silently
- Low top_score (<0.010): embedding model mismatch or query is too narrow
- Good chunks but wrong module: source_type or module metadata not set during ingestion
- Tavily fallback in local mode: web search being used when it shouldn't

For each finding, provide:
- What you found
- Why it's a problem
- How to fix it (specific command or configuration change)"""
```

---

## Agent 8: Internal Banner Chatbot Agent

**Purpose:** Internal-user chatbot for IT staff, functional analysts, and Banner admins.
Wraps the `/chat/*` adapter endpoints. Answers questions about Banner ERP modules,
release notes, upgrade changes, and operational SOPs. Escalates when confidence is low.

**Use case:** Deployed as a Botpress webhook target — internal users ask in natural language
and get grounded answers citing release notes or SOP documents directly.

**Type:** Multi-tool agent with escalation logic. Uses the simplified three-source routing
(`banner`, `finance`, `sop`).

### Tools

```python
INTERNAL_CHATBOT_TOOLS = [
    {
        "name": "chat_intent",
        "description": (
            "Classify the user's message into one of: BannerRelease, BannerFinance, "
            "SopQuery, BannerAdmin, General. "
            "Returns intent name and confidence score (0–1). "
            "Use this first to decide which source to query."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "message": {"type": "string", "description": "The user's question"},
            },
            "required": ["message"],
        },
    },
    {
        "name": "chat_sentiment",
        "description": (
            "Detect the emotional tone of the user's message. "
            "Returns sentiment (Positive, Neutral, Frustrated) and a score (0–1). "
            "Use this to decide whether to escalate regardless of answer confidence."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "message": {"type": "string"},
            },
            "required": ["message"],
        },
    },
    {
        "name": "chat_ask",
        "description": (
            "Answer the user's question using the Ask Banner knowledge base. "
            "Set source based on the intent: "
            "'finance' for BannerFinance, 'sop' for SopQuery, "
            "'banner' for everything else (BannerRelease, BannerAdmin, General). "
            "Returns answer, confidence (0–1), sources[], and escalate flag."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "message": {"type": "string", "description": "The user's question verbatim"},
                "session_id": {"type": "string", "description": "Unique session identifier"},
                "source": {
                    "type": "string",
                    "enum": ["banner", "finance", "sop"],
                    "description": "Routing source derived from intent",
                },
            },
            "required": ["message", "session_id", "source"],
        },
    },
]
```

### System Prompt

```python
INTERNAL_CHATBOT_SYSTEM = """You are Ask Banner, an internal knowledge assistant for
IT staff, functional analysts, and Banner administrators. You answer questions about
Ellucian Banner ERP — release notes, module changes, upgrade procedures, and SOPs.

Workflow for every user message:
1. Call chat_intent to classify the message and get a confidence score.
2. Call chat_sentiment to detect frustration.
3. Map intent to source:
   - BannerFinance → source=finance
   - SopQuery      → source=sop
   - BannerRelease, BannerAdmin, General → source=banner
4. Call chat_ask with the message, session_id, and source.
5. If resp.escalate == true OR sentiment == Frustrated:
   Reply: "I wasn't able to find a confident answer. Please check the source documents
   directly or escalate to the Banner team lead."
   Do not attempt to answer further.
6. Otherwise: deliver the answer concisely. Always cite the document title, page, and
   version if available. Technical accuracy matters more than brevity.

Rules:
- Never answer from prior training knowledge alone — always call chat_ask first.
- If the question is unrelated to Banner or SOPs, reply: "I can only help with Banner
  ERP questions and SOP procedures."
- Do not expose source URLs, API calls, or internal routing details.
"""
```

### Implementation

```python
import os
import anthropic

ADAPTER_BASE_URL = os.environ["ADAPTER_BASE_URL"]  # e.g. http://localhost:8080

ADAPTER_HEADERS = {"Content-Type": "application/json"}

client = anthropic.Anthropic()

def run_adapter_tool(tool_name: str, tool_input: dict) -> str:
    dispatch = {
        "chat_intent":    lambda i: requests.post(f"{ADAPTER_BASE_URL}/chat/intent", json=i, headers=ADAPTER_HEADERS).json(),
        "chat_sentiment": lambda i: requests.post(f"{ADAPTER_BASE_URL}/chat/sentiment", json=i, headers=ADAPTER_HEADERS).json(),
        "chat_ask":       lambda i: requests.post(f"{ADAPTER_BASE_URL}/chat/ask", json=i, headers=ADAPTER_HEADERS).json(),
    }
    return json.dumps(dispatch[tool_name](tool_input))

def internal_chatbot_agent(message: str, session_id: str) -> str:
    messages = [{"role": "user", "content": message}]

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=512,
            system=INTERNAL_CHATBOT_SYSTEM,
            tools=INTERNAL_CHATBOT_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")

        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                inp = dict(block.input)
                if block.name == "chat_ask":
                    inp["session_id"] = session_id
                result = run_adapter_tool(block.name, inp)
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })

        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example Session

```
User: What database changes are required for Banner General 9.3.37?

Agent: [calls chat_intent("What database changes are required for Banner General 9.3.37?")]
       → {intent: "BannerRelease", confidence: 0.90}
       [calls chat_sentiment("...")]
       → {sentiment: "Neutral", score: 0.05}
       [calls chat_ask(message="...", session_id="abc", source="banner")]
       → {answer: "Banner General 9.3.37 requires running GENUPGRADE.sql before...",
          confidence: 0.82, escalate: false}

Answer: Banner General 9.3.37 requires the following database changes before restarting:
1. Run GENUPGRADE.sql (DBA checklist, page 4)
2. Update three indexes on the GZRSTAT table
Confidence: 0.82 — Source: Banner General Release Notes 9.3.37, page 4
```

```
User: Is there an SOP for the post-upgrade smoke test?

Agent: [calls chat_intent → {intent: "SopQuery", confidence: 0.85}]
       [calls chat_ask(source="sop")]
       → {answer: "SOP122 covers the post-upgrade smoke test...", confidence: 0.81}

Answer: Yes — SOP122 (Smoke Test and Sanity Test Post Banner Upgrade) covers this.
Section 3 has the full checklist. (Source: SOP122, Section 3, confidence: 0.81)
```

### Escalation Example

```
User: THE INDEX IS BROKEN NOTHING RETURNS RESULTS THIS IS USELESS

Agent: [calls chat_sentiment → {sentiment: "Frustrated", score: 0.94}]
       [skips answering due to frustration]

Answer: I wasn't able to find a confident answer. Please check the source documents
directly or escalate to the Banner team lead.
```

---

## Agent 9: Confidence Calibration Agent

**Purpose:** Determine the correct escalate score floor for `internal/adapter/client.go` by
running a structured calibration protocol against the live backend. Re-run whenever the index
grows significantly (new Banner versions ingested) since score distributions shift with corpus size.

**Use case:** After a new ingestion, or whenever the escalate behavior seems wrong, run this agent
to get a data-driven threshold recommendation instead of guessing.

**Type:** Diagnostic protocol agent. Sequential tool calls with structured output.

### Tools

Reuses Agent 7's tools: `banner_ask` and `index_stats`.

```python
CALIBRATION_TOOLS = [
    {
        "name": "index_stats",
        "description": "Get current index chunk count and metadata.",
        "input_schema": {"type": "object", "properties": {}},
    },
    {
        "name": "banner_ask",
        "description": "Run a test query and return sources with scores.",
        "input_schema": {
            "type": "object",
            "properties": {
                "question": {"type": "string"},
                "module_filter": {"type": "string"},
                "top_k": {"type": "integer", "default": 3},
            },
            "required": ["question"],
        },
    },
]
```

### Calibration Query Sets

```python
# Known-good queries: these SHOULD return useful answers if the index is populated
KNOWN_GOOD = [
    {"question": "What changed in Banner General?", "module_filter": "General"},
    {"question": "What are the Banner General release notes?", "module_filter": "General"},
    {"question": "What is new in Banner 9.3.37.2?", "module_filter": "General"},
    {"question": "What support changes were made in Banner 8?", "module_filter": "General"},
    {"question": "What are the breaking changes in the Banner General release?", "module_filter": "General"},
]

# Known-boundary queries: these SHOULD have low or zero results
KNOWN_BOUNDARY = [
    {"question": "What changed in Banner Finance module?", "module_filter": "Finance"},
    {"question": "Banner Student 9.4.1 release notes", "module_filter": "Student"},
    {"question": "Banner HR payroll changes", "module_filter": "HR"},
    {"question": "What is the weather today?", "module_filter": "General"},
    {"question": "Who is the CEO of Ellucian?", "module_filter": "General"},
]
```

### System Prompt

```python
CALIBRATION_SYSTEM = """You are a confidence score calibration agent for an Azure AI Search-backed
RAG system. Your goal is to determine the correct escalate threshold for the adapter layer.

Calibration protocol:
1. Call index_stats to record the current index state (chunk count, index name).
2. Run each query in KNOWN_GOOD (passed as context) using banner_ask. Record:
   - retrieval_count
   - sources[0].score (or 0 if sources is empty)
   - Whether the answer text appears useful (not empty, not "no documents found")
3. Run each query in KNOWN_BOUNDARY. Record the same fields.
4. Analyze the data:
   - What is the MINIMUM score among known-good queries with useful answers?
   - What is the MAXIMUM score among known-boundary queries with useless/empty answers?
   - Is there a clean gap between those two groups?
5. Recommend a threshold:
   - If gap exists: set floor = midpoint of the gap, rounded down to nearest 0.005
   - If no gap (scores overlap): recommend threshold = 0.0 (use retrieval_count == 0 only)
   - Never recommend above 0.05 without strong justification

Output a Markdown calibration report:
## Calibration Report — [date] — [index_name]
### Index State
[chunk count, index name]
### Known-Good Query Results
| Query | module | retrieval_count | score | useful? |
### Known-Boundary Query Results
[same table]
### Score Distribution Analysis
[min good score, max boundary score, gap or overlap]
### Recommendation
**Threshold:** 0.0XX
**Rationale:** [1–2 sentences]
**Next calibration:** [recommend after N more documents ingested or N months]
"""
```

### Implementation

```python
def calibration_agent(module_filter: str = "General") -> str:
    context = f"""
Run the calibration protocol for module_filter='{module_filter}'.

KNOWN_GOOD queries:
{json.dumps(KNOWN_GOOD, indent=2)}

KNOWN_BOUNDARY queries:
{json.dumps(KNOWN_BOUNDARY, indent=2)}

Produce a full calibration report as described in your instructions.
"""
    messages = [{"role": "user", "content": context}]

    while True:
        response = client.messages.create(
            model="claude-haiku-4-5-20251001",
            max_tokens=2048,
            system=CALIBRATION_SYSTEM,
            tools=CALIBRATION_TOOLS,
            messages=messages,
        )

        if response.stop_reason == "end_turn":
            return next(b.text for b in response.content if b.type == "text")

        tool_results = []
        for block in response.content:
            if block.type == "tool_use":
                result = run_tool(block.name, dict(block.input))
                tool_results.append({
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": result,
                })
        messages.append({"role": "assistant", "content": response.content})
        messages.append({"role": "user", "content": tool_results})
```

### Example Output

```
## Calibration Report — 2026-04-16 — banner-upgrade-knowledge

### Index State
- Chunks: 1,247  Index: banner-upgrade-knowledge

### Known-Good Query Results
| Query | module | retrieval_count | score | useful? |
|-------|--------|----------------|-------|---------|
| What changed in Banner General? | General | 3 | 0.033 | yes |
| Banner General release notes? | General | 5 | 0.029 | yes |
| What is new in 9.3.37.2? | General | 4 | 0.041 | yes |
| Support changes in Banner 8? | General | 2 | 0.027 | yes |
| Breaking changes in Banner General? | General | 3 | 0.035 | yes |

### Known-Boundary Query Results
| Query | module | retrieval_count | score | useful? |
|-------|--------|----------------|-------|---------|
| Banner Finance module? | Finance | 0 | 0.000 | no |
| Banner Student 9.4.1? | Student | 0 | 0.000 | no |
| Banner HR payroll? | HR | 0 | 0.000 | no |
| What is the weather today? | General | 1 | 0.004 | no |
| Who is the CEO of Ellucian? | General | 1 | 0.006 | no |

### Score Distribution Analysis
- Min good score: 0.027  Max boundary score: 0.006
- Clear gap exists: 0.006 → 0.027 (gap of 0.021)
- Gap midpoint: 0.016

### Recommendation
**Threshold:** 0.010
**Rationale:** Clean gap between useful answers (≥ 0.027) and noise (≤ 0.006).
Threshold of 0.010 sits safely in the middle with margin. Rounds down to nearest 0.005.
**Next calibration:** After 500+ new chunks ingested, or if escalate behavior seems off.
```

---

## Tool Reference: API-to-Tool Mapping

| Tool Name | HTTP Method | Endpoint | Agent(s) Using It |
|-----------|-------------|----------|--------------------|
| `banner_ask` | POST | `/banner/ask` | 1, 3, 4, 6, 7, 9 |
| `sop_ask` | POST | `/sop/ask` | 2, 3, 4, 5, 6 |
| `sop_list` | GET | `/sop` | 2, 4, 6 |
| `banner_summarize` | POST | `/banner/summarize/{topic}` | 3, 4, 6 |
| `banner_ingest` | POST | `/banner/ingest` | 5 |
| `sop_ingest` | POST | `/sop/ingest` | 5 |
| `index_health` | GET | `/health` | 5, 7 |
| `index_stats` | GET | `/index/stats` | 5, 7, 9 |
| `chat_ask` | POST | `/chat/ask` | 8 |
| `chat_intent` | POST | `/chat/intent` | 8 |
| `chat_sentiment` | POST | `/chat/sentiment` | 8 |

---

## Implementation Notes

### Project Structure (suggested)

```
agents/
├── __init__.py
├── common.py          # run_tool dispatcher, API client setup
├── banner_ask.py      # Agent 1
├── sop_navigator.py   # Agent 2
├── upgrade_analyzer.py # Agent 3
├── conversational.py  # Agent 4
├── ingestion.py       # Agent 5
├── gap_analyzer.py    # Agent 6
├── diagnostics.py     # Agent 7
└── cli.py             # Entry point: pick agent by command-line arg
```

### Confidence Escalation (all agents)

The existing backend already returns `top_score` and `retrieval_count` in every response.
All agents should enforce this pattern:

```python
def check_confidence(api_result: dict) -> str | None:
    """Returns an escalation message if confidence is too low, else None."""
    if api_result.get("retrieval_count", 0) == 0:
        return "No documents found in the index. Check that ingestion has been run."
    if api_result.get("top_score", 0) < 0.01:
        return f"Near-zero score ({api_result['top_score']:.3f}). Verify answer against source docs."
    # Note: scores of 0.01–0.05 are normal for this index — not low confidence
    return None
```

### Cost Control

| Agent | Recommended Model | Typical Cost per Run |
|-------|------------------|---------------------|
| Banner Ask (Agent 1) | `claude-haiku-4-5` | < $0.001 |
| SOP Navigator (Agent 2) | `claude-haiku-4-5` | < $0.001 |
| Upgrade Analyzer (Agent 3) | `claude-sonnet-4-6` | ~$0.01–0.05 |
| Conversational (Agent 4) | `claude-sonnet-4-6` | ~$0.005 per turn |
| Ingestion Orchestrator (Agent 5) | `claude-haiku-4-5` | < $0.001 |
| Gap Analyzer (Agent 6) | `claude-sonnet-4-6` | ~$0.05–0.10 |
| Diagnostics (Agent 7) | `claude-haiku-4-5` | < $0.005 |
| Internal Banner Chatbot (Agent 8) | `claude-haiku-4-5` | < $0.002 per turn |

Azure OpenAI costs (embedding + chat) remain the same regardless of which Claude model is used.

### Testing Agents Without Claude API Costs

Use `anthropic.Anthropic(api_key="fake")` and mock `client.messages.create` to test the
tool dispatch loop and prompt logic without incurring API costs.

---

## Priority Recommendation

**Start here (highest value, lowest complexity):**

1. **Agent 1 — Banner Ask**: One tool, immediate utility, validates the end-to-end flow
2. **Agent 2 — SOP Navigator**: One additional tool, complements Agent 1

**Next (unique capability not available today):**

3. **Agent 3 — Upgrade Impact Analyzer**: Automates what currently takes hours of manual reading
4. **Agent 7 — Diagnostics**: Saves time when something breaks

**After the above are working:**

5. **Agent 4 — Conversational**: Requires more careful session management
6. **Agent 5 — Ingestion Orchestrator**: Useful but ingestion is currently rare
7. **Agent 6 — Gap Analyzer**: High value for upgrade planning but complex to validate

**Primary chatbot (internal users — this is the main Botpress target):**

8. **Agent 8 — Internal Banner Chatbot**: Wraps the adapter `/chat/*` layer with intent
   classification, sentiment-aware escalation, and a Banner-admin persona. This is the
   agent Botpress calls.

---

*Related wiki documents: [INTEGRATIONS.md](INTEGRATIONS.md) covers LangGraph and n8n alternatives.
These Claude agents are the simplest path — no separate orchestration infrastructure required.*
