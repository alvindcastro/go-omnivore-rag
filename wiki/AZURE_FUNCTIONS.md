# Azure Functions as Consumers — Brainstorm

Ideas for wiring Azure Functions into go-omnivore-rag as event-driven consumers.
Azure Functions fits naturally here because the project already lives in the Azure ecosystem
(OpenAI, AI Search, Blob Storage — same subscription, same networking, same identity model).

---

## Table of Contents

1. [Why Azure Functions for This Project](#why-azure-functions-for-this-project)
2. [Prerequisites](#prerequisites)
3. [Hosting Plan Decision](#hosting-plan-decision)
4. [Trigger Inventory and Fit](#trigger-inventory-and-fit)
5. [Blob Storage Trigger → Auto-Ingest](#blob-storage-trigger--auto-ingest)
6. [Timer Trigger → Scheduled Jobs](#timer-trigger--scheduled-jobs)
7. [HTTP Trigger → API Gateway / Proxy](#http-trigger--api-gateway--proxy)
8. [Service Bus → Async Ingestion Queue](#service-bus--async-ingestion-queue)
9. [Event Grid → Document Event Fan-Out](#event-grid--document-event-fan-out)
10. [Durable Functions → Orchestrated Workflows](#durable-functions--orchestrated-workflows)
11. [Queue Storage Trigger → Simple Async Jobs](#queue-storage-trigger--simple-async-jobs)
12. [Cosmos DB Change Feed → Metadata Sync](#cosmos-db-change-feed--metadata-sync)
13. [Event Hub Trigger → High-Volume Streaming Ingest](#event-hub-trigger--high-volume-streaming-ingest)
14. [Output Bindings: What to Do After a RAG Call](#output-bindings-what-to-do-after-a-rag-call)
15. [Managed Identity — No API Keys](#managed-identity--no-api-keys)
16. [Go Custom Handlers](#go-custom-handlers)
17. [Application Insights Integration](#application-insights-integration)
18. [Full Architecture: Everything Connected](#full-architecture-everything-connected)
19. [Implementation Priority](#implementation-priority)
20. [What Needs to Be Built in go-omnivore-rag First](#what-needs-to-be-built-in-go-omnivore-rag-first)

---

## Why Azure Functions for This Project

go-omnivore-rag already uses three Azure services: OpenAI, AI Search, and Blob Storage.
Azure Functions lives in the same subscription, uses the same networking primitives (VNet,
Private Endpoints), and authenticates via the same identity model (Managed Identity / Entra ID).

**What Azure Functions adds that the current system lacks:**
- Automatic triggering when documents arrive in Blob Storage (no polling, no manual ingest)
- Reliable async ingestion via Service Bus queues (retry, dead-letter, backpressure)
- Parallel summarization workflows via Durable Functions fan-out
- Real-time push to web clients via SignalR output binding
- Centralized monitoring in Application Insights alongside the rest of the Azure stack

**What Azure Functions is NOT for here:**
- Replacing the go-omnivore-rag HTTP server — the REST API stays as-is
- Complex agent reasoning — that's LangGraph's job
- Business process automation with 20+ steps — use Logic Apps for that

The Functions call go-omnivore-rag over HTTP. They are consumers, not replacements.

---

## Prerequisites

Before any Function can call go-omnivore-rag:

### 1. Deploy go-omnivore-rag to Azure

Azure Functions running in Azure cannot reach `localhost:8000`. Options:
- **Azure Container Apps** (recommended) — deploy the Docker image, get a stable FQDN
- **Azure App Service** — simpler, but less control
- **Azure VM** in the same VNet — most control, most ops overhead

Minimum: build a `Dockerfile`, push to Azure Container Registry, deploy to Container Apps.

### 2. API Key Auth on go-omnivore-rag

Every Function will send HTTP requests. go-omnivore-rag needs to validate them.
Store the key in Azure Key Vault. Functions fetch it at startup via Managed Identity — no secrets
in environment variables.

### 3. Private Networking (Optional but Recommended)

Put go-omnivore-rag on a VNet. Azure Functions (Flex Consumption or Premium plan) can join
the same VNet via VNet Integration. Functions call the RAG API over the private network — no
public internet exposure needed.

---

## Hosting Plan Decision

| Plan | Use When |
|------|----------|
| **Flex Consumption** | Variable load, need VNet, want reduced cold starts, cost-sensitive. Best default choice. |
| **Premium** | Consistent baseline load, long-running Durable workflows, need guaranteed warmth. |
| **App Service** | Running alongside other App Service workloads. Less common for Functions-only. |
| **Consumption (legacy)** | Avoid — no VNet, cold start issues, being superseded by Flex Consumption. |

**Recommendation: Flex Consumption.** It supports VNet integration (needed for private RAG API
access), has a free monthly grant (250K executions + 100K GB-s), and supports Durable Functions.

---

## Trigger Inventory and Fit

| Trigger | Fit for go-omnivore-rag | Priority |
|---------|------------------------|----------|
| Blob Storage | Excellent — directly connected to existing blob container | High |
| Timer | Excellent — scheduled sync and reporting | High |
| HTTP | Good — API gateway / proxy layer | Medium |
| Service Bus | Good — reliable async ingestion queue | Medium |
| Event Grid | Good — fan-out to multiple consumers on blob events | Medium |
| Durable Functions | Excellent — parallel summarization, workflow orchestration | High |
| Queue Storage | Good — simpler alternative to Service Bus | Low |
| Cosmos DB Change Feed | Niche — only if metadata store added | Low |
| Event Hub | Niche — only at high document volume | Low |

---

## Blob Storage Trigger → Auto-Ingest

**The single most valuable integration.** Today, someone must manually call `POST /banner/ingest`
after uploading PDFs to Blob Storage. A Blob trigger eliminates that step entirely.

### How It Works

```
Someone uploads Banner_General_9.3.38_ReleaseNotes.pdf
to Azure Blob container "banner-release-notes"
        ↓
Azure Event Grid fires BlobCreated event (near real-time)
        ↓
Azure Function: BlobIngestTrigger
        ↓
Determine document type from blob path/name
├── path contains "/sop/" → call POST /sop/ingest
└── .pdf extension       → call POST /banner/ingest
        ↓
Log result to Application Insights
        ↓
Output binding → Service Bus message → downstream consumers notified
```

### Function sketch (Python)

```python
import azure.functions as func
import requests, logging, os

app = func.FunctionApp()

@app.blob_trigger(
    arg_name="blob",
    path="banner-release-notes/{name}",
    connection="AzureWebJobsStorage"
)
def blob_ingest_trigger(blob: func.InputStream):
    blob_name = blob.name
    logging.info(f"New blob detected: {blob_name}")

    base_url = os.environ["OMNIVORE_RAG_BASE_URL"]
    api_key  = os.environ["OMNIVORE_RAG_API_KEY"]
    headers  = {"Authorization": f"Bearer {api_key}"}

    if "/sop/" in blob_name.lower() or blob_name.lower().endswith(".docx"):
        endpoint = f"{base_url}/sop/ingest"
        payload  = {"overwrite": False}
    else:
        endpoint = f"{base_url}/banner/ingest"
        payload  = {"overwrite": False, "docs_path": "data/docs/banner"}

    response = requests.post(endpoint, json=payload, headers=headers, timeout=300)
    response.raise_for_status()
    logging.info(f"Ingest complete: {response.json()}")
```

### Important Notes

- Use the **Event Grid-based** blob trigger (extension v5.x+), not the polling-based one.
  The polling version checks every few seconds; Event Grid fires in milliseconds.
- The Function does not receive the file bytes — it receives the blob name. go-omnivore-rag
  already knows how to read from Blob Storage via `/banner/blob/sync`. The Function just signals
  it to start.
- Set a generous `timeout` — ingestion can take minutes for large PDFs.

---

## Timer Trigger → Scheduled Jobs

Replace manual operations with scheduled automation.

### Job 1: Daily Blob Sync (Replace Manual /blob/sync)

```python
@app.schedule(
    schedule="0 0 2 * * *",   # 2:00 AM daily (NCRONTAB)
    arg_name="timer",
    run_on_startup=False
)
def daily_blob_sync(timer: func.TimerRequest):
    """Pull new documents from Blob and ingest them."""
    response = requests.post(f"{BASE_URL}/banner/blob/sync",
                             json={"overwrite": False}, headers=HEADERS, timeout=600)
    # Output binding: post result to Service Bus topic for downstream notification
```

### Job 2: Weekly Upgrade Readiness Report

```python
@app.schedule(schedule="0 0 8 * * 1")  # Monday 08:00
def weekly_report(timer: func.TimerRequest):
    versions = ["9.3.37.2", "9.3.38.0"]  # or read from Cosmos DB / config
    reports = []
    for version in versions:
        r = requests.post(f"{BASE_URL}/banner/summarize/full",
                          json={"version": version, "top_k": 20},
                          headers=HEADERS, timeout=120)
        reports.append(r.json())
    # Send to Logic App or email via Communication Services
```

### Job 3: Index Health Check

```python
@app.schedule(schedule="0 */30 * * * *")  # Every 30 minutes
def health_check(timer: func.TimerRequest):
    health = requests.get(f"{BASE_URL}/health", headers=HEADERS).json()
    stats  = requests.get(f"{BASE_URL}/index/stats", headers=HEADERS).json()
    if stats.get("total_chunks", 0) < EXPECTED_MINIMUM_CHUNKS:
        # Output binding: alert to Service Bus → notification pipeline
        logging.critical(f"Index too small: {stats}")
```

### NCRONTAB Format (Azure Functions)

```
{second} {minute} {hour} {day} {month} {day-of-week}
"0 0 2 * * *"     → 2:00:00 AM every day
"0 0 8 * * 1"     → 8:00 AM every Monday
"0 */30 * * * *"  → Every 30 minutes
"0 0 0 1 * *"     → Midnight on the 1st of each month
```

---

## HTTP Trigger → API Gateway / Proxy

An HTTP-triggered Function in front of go-omnivore-rag adds a layer for auth, caching,
routing, and transformation without modifying the Go codebase.

### Pattern: Auth + Rate Limiting Proxy

```
External client (Teams bot, web UI, etc.)
        ↓
Azure Function: OmnivoreProxy (HTTP trigger)
├── Validate JWT / API key
├── Check rate limit (counter in Azure Cache for Redis or Table Storage)
├── Log request with correlation ID
├── Forward to go-omnivore-rag with internal API key
├── Post-process response (filter fields, add metadata)
└── Return to client
        ↓
go-omnivore-rag (internal, no public endpoint)
```

**Why this is useful:** go-omnivore-rag has no auth. Rather than adding auth middleware to every
handler in Go, put a Function in front that handles it once. The Go API stays simple.

### Pattern: Azure API Management + Functions

If the team uses APIM already:
- Import go-omnivore-rag's Swagger spec into APIM
- APIM handles auth, rate limiting, analytics dashboard
- Functions handle pre/post processing that APIM policies can't express cleanly
- Clients call `https://omnivore.azure-api.net/banner/ask` instead of the raw API

---

## Service Bus → Async Ingestion Queue

For high-volume document scenarios: instead of calling `/ingest` directly (which blocks for
minutes), drop a message on a queue and let a Function process it asynchronously.

### Architecture

```
Document uploaded to Blob
        ↓
Event Grid → Service Bus Queue: "ingest-jobs"
        ↓
Azure Function: IngestWorker (Service Bus trigger)
├── Dequeue message: { "blob_path": "...", "doc_type": "banner" }
├── Call POST /banner/ingest or /sop/ingest
├── On success: complete message (remove from queue)
└── On failure: abandon message (retry up to MaxDeliveryCount=5)
                After 5 failures → Dead-Letter Queue
                        ↓
        Azure Function: DLQMonitor alerts on dead-lettered messages
```

### Why Service Bus Over Direct HTTP Calls

| Direct HTTP | Service Bus Queue |
|-------------|-----------------|
| Blob trigger calls /ingest directly | Blob trigger puts message on queue |
| If ingest fails, retry logic is manual | Service Bus retries automatically (up to N times) |
| No visibility into queue depth | Azure portal shows queue depth, DLQ count |
| Parallel ingestion can overload the RAG API | Queue acts as backpressure valve |
| No dead-letter audit trail | DLQ captures every failed message with reason |

### Message Schema

```json
{
  "blob_path": "banner-release-notes/general/2026/february/Banner_General_9.3.38.pdf",
  "doc_type": "banner",
  "overwrite": false,
  "requested_by": "upload-portal",
  "correlation_id": "abc-123",
  "enqueued_at": "2026-03-26T14:30:00Z"
}
```

### Topic + Subscriptions (for fan-out)

Use a Service Bus **Topic** instead of a Queue if multiple consumers need to act on each ingest:

```
Service Bus Topic: "document-ingested"
├── Subscription: "rag-ingest"   → Function calls /ingest
├── Subscription: "notifier"     → Function sends Slack/Teams notification
└── Subscription: "audit-log"   → Function writes to Cosmos DB audit table
```

---

## Event Grid → Document Event Fan-Out

Azure Event Grid provides a push model for blob events. Unlike Service Bus (pull), Event Grid
pushes events to all subscribers simultaneously.

### Event Flow

```
Blob Storage: "banner-release-notes" container
        ↓  (BlobCreated / BlobDeleted events)
Azure Event Grid Topic
        ├── Subscriber 1: Azure Function → POST /banner/ingest
        ├── Subscriber 2: Azure Function → send Teams notification
        ├── Subscriber 3: Logic App → create Jira ticket
        └── Subscriber 4: Azure Function → update document registry in Cosmos DB
```

### When to Prefer Event Grid Over Blob Trigger

The modern blob trigger (v5.x+) uses Event Grid internally anyway. Use a standalone Event Grid
subscription when:
- Multiple Azure services need to react to the same blob event
- You want to filter events (only `.pdf` files, only specific container paths)
- You need to replay events or inspect event history

### Event Filter Example

```json
{
  "subjectBeginsWith": "/blobServices/default/containers/banner-release-notes/blobs/general/",
  "subjectEndsWith": ".pdf",
  "includedEventTypes": ["Microsoft.Storage.BlobCreated"]
}
```

Only fires for PDFs uploaded to the `general/` prefix — ignores test uploads in other folders.

---

## Durable Functions → Orchestrated Workflows

Durable Functions are the Azure-native equivalent of LangGraph's stateful graphs. They use
orchestrator + activity functions to build reliable multi-step workflows.

### Pattern 1: Parallel Summarization (Fan-Out / Fan-In)

Generate all four Banner summaries in parallel, then combine.

```python
@app.orchestration_trigger(context_name="context")
def upgrade_analysis_orchestrator(context: df.DurableOrchestrationContext):
    version = context.get_input()

    # Fan-out: fire all four summarize calls in parallel
    tasks = [
        context.call_activity("SummarizeChanges",      {"version": version}),
        context.call_activity("SummarizeBreaking",     {"version": version}),
        context.call_activity("SummarizeActions",      {"version": version}),
        context.call_activity("SummarizeCompatibility",{"version": version}),
    ]
    results = yield context.task_all(tasks)   # Fan-in: wait for all four

    # Synthesize into final report
    report = yield context.call_activity("BuildReport", {
        "version": version,
        "changes": results[0],
        "breaking": results[1],
        "actions": results[2],
        "compatibility": results[3],
    })
    return report

@app.activity_trigger(input_name="params")
def summarize_changes(params: dict) -> dict:
    r = requests.post(f"{BASE_URL}/banner/summarize/changes",
                      json={"version": params["version"], "top_k": 20},
                      headers=HEADERS, timeout=120)
    return r.json()
```

**What this buys over calling `/banner/summarize/full`:** Each activity runs independently and
retries individually. If the Actions call fails, only that activity retries — not the entire
workflow. The full summary endpoint is all-or-nothing.

---

### Pattern 2: Sequential Ingestion Pipeline (Chaining)

```python
@app.orchestration_trigger(context_name="context")
def ingest_pipeline_orchestrator(context: df.DurableOrchestrationContext):
    blob_path = context.get_input()

    # Step 1: Validate document
    metadata = yield context.call_activity("ValidateDocument", blob_path)
    if not metadata["valid"]:
        return {"status": "rejected", "reason": metadata["reason"]}

    # Step 2: Ingest
    ingest_result = yield context.call_activity("IngestDocument", metadata)

    # Step 3: Verify chunks were created
    stats = yield context.call_activity("VerifyIngestion", ingest_result)

    # Step 4: Notify
    yield context.call_activity("SendNotification", {
        "doc": blob_path,
        "chunks_indexed": stats["chunks"]
    })

    return {"status": "complete", "chunks": stats["chunks"]}
```

---

### Pattern 3: Human Approval Before Destructive Operations

```python
@app.orchestration_trigger(context_name="context")
def approval_orchestrator(context: df.DurableOrchestrationContext):
    operation = context.get_input()  # e.g., {"action": "recreate_index"}

    # Send approval request to Teams/email
    yield context.call_activity("SendApprovalRequest", {
        "approver": "it-manager@company.com",
        "operation": operation,
        "approval_url": context.instance_id   # used to resume orchestration
    })

    # Wait up to 24 hours for approval
    approval_event = context.wait_for_external_event("ApprovalDecision")
    timeout = context.create_timer(context.current_utc_datetime + timedelta(hours=24))
    result = yield context.task_any([approval_event, timeout])

    if result == approval_event and result.result["approved"]:
        yield context.call_activity("RecreateIndex", operation)
        return {"status": "executed"}
    else:
        return {"status": "timed_out_or_rejected"}

# HTTP trigger to receive approval from email link / Teams button
@app.route(route="approve/{instanceId}")
def receive_approval(req: func.HttpRequest, instanceId: str) -> func.HttpResponse:
    client = df.DurableOrchestrationClient(...)
    client.raise_event(instanceId, "ApprovalDecision", {"approved": True})
    return func.HttpResponse("Approved.")
```

---

### Pattern 4: Monitor / Polling Loop

For long-running ingestion jobs, poll the index stats until the expected chunk count is reached.

```python
@app.orchestration_trigger(context_name="context")
def ingest_monitor_orchestrator(context: df.DurableOrchestrationContext):
    params = context.get_input()
    expected_min_chunks = params["expected_min_chunks"]

    # Trigger ingestion
    yield context.call_activity("TriggerIngest", params)

    # Poll every 30 seconds until chunks appear (max 10 checks = 5 minutes)
    for attempt in range(10):
        stats = yield context.call_activity("GetIndexStats", None)
        if stats["total_chunks"] >= expected_min_chunks:
            return {"status": "complete", "chunks": stats["total_chunks"]}

        next_check = context.current_utc_datetime + timedelta(seconds=30)
        yield context.create_timer(next_check)

    return {"status": "timed_out", "chunks": stats["total_chunks"]}
```

---

## Queue Storage Trigger → Simple Async Jobs

Azure Queue Storage is simpler than Service Bus: no topics, no DLQ management, no sessions.
Good for low-volume, low-criticality async tasks.

```python
@app.queue_trigger(
    arg_name="msg",
    queue_name="rag-ingest-jobs",
    connection="AzureWebJobsStorage"
)
def queue_ingest_worker(msg: func.QueueMessage):
    job = msg.get_json()
    endpoint = "/sop/ingest" if job["doc_type"] == "sop" else "/banner/ingest"
    requests.post(f"{BASE_URL}{endpoint}", json={"overwrite": False}, headers=HEADERS)
```

Azure Queue Storage automatically retries messages that fail (up to 5 times by default),
then moves them to `<queue-name>-poison` for inspection. No configuration needed.

**Use Queue Storage when:** low volume (< 10 ingest jobs/day), you want simplicity over features.
**Use Service Bus when:** you need DLQ management, multiple subscribers, sessions, or ordering.

---

## Cosmos DB Change Feed → Metadata Sync

If a document registry is stored in Cosmos DB (tracking which Banner versions are indexed,
with what metadata), the Change Feed trigger fires on every insert/update.

```
Document registry updated in Cosmos DB
("Banner General 9.3.38 — status: uploaded")
        ↓
Azure Function: RegistryChangeFeed
        ↓
If status == "uploaded" and not yet indexed:
    POST /banner/ingest
        ↓
Update Cosmos DB record: status = "indexed", chunks = N
```

**This pattern is only relevant** if you add a Cosmos DB metadata store. Currently the project
has no persistent state outside of Azure AI Search. Cosmos DB would add: document registry,
ingestion history, audit log, per-user conversation history (for LangGraph memory).

---

## Event Hub Trigger → High-Volume Streaming Ingest

Event Hub is designed for millions of events per second. Overkill for this project at current
scale, but the pattern is worth knowing.

```
High-volume document pipeline (e.g., daily batch of 500 release notes)
        ↓
Each document arrival → Event Hub message
        ↓
Azure Function scales horizontally (one instance per partition)
        ↓
Each instance calls /banner/ingest for its partition's documents
        ↓
Checkpointing via Azure Storage → no duplicate processing
```

**When this matters:** If go-omnivore-rag is extended to index a full document management system
(thousands of documents per day), Event Hub provides the throughput that Blob triggers and Service
Bus cannot.

---

## Output Bindings: What to Do After a RAG Call

After a Function calls go-omnivore-rag and gets a response, output bindings push the result
downstream without additional HTTP calls.

### Service Bus Output: Notify Downstream

```python
@app.queue_trigger(arg_name="msg", queue_name="ask-jobs", ...)
@app.service_bus_topic_output(
    arg_name="notification",
    connection="ServiceBusConnection",
    topic_name="rag-results"
)
def process_ask_job(msg: func.QueueMessage, notification: func.Out[str]):
    job = msg.get_json()
    result = requests.post(f"{BASE_URL}/banner/ask", json=job, headers=HEADERS).json()
    notification.set(json.dumps({
        "job_id": job["correlation_id"],
        "answer": result["answer"],
        "sources": result["sources"]
    }))
```

### Cosmos DB Output: Persist Q&A History

```python
@app.cosmos_db_output(
    arg_name="qa_record",
    database_name="omnivore",
    container_name="qa-history",
    connection="CosmosDBConnection"
)
def process_ask_job(msg, qa_record: func.Out[func.Document]):
    result = call_rag_api(msg.get_json())
    qa_record.set(func.Document.from_dict({
        "id": str(uuid.uuid4()),
        "question": result["question"],
        "answer": result["answer"],
        "sources": result["sources"],
        "timestamp": datetime.utcnow().isoformat(),
        "user_id": msg.get_json().get("user_id")
    }))
```

### SignalR Output: Push to Web Clients in Real Time

Stream answers to a browser as they arrive — no polling required.

```python
@app.signalr_output(
    arg_name="signal_msg",
    hub_name="omnivoreHub",
    connection="AzureSignalRConnectionString"
)
def deliver_answer(msg, signal_msg: func.Out[str]):
    result = call_rag_api(msg.get_json())
    signal_msg.set(json.dumps({
        "target": "receiveAnswer",
        "arguments": [result["answer"], result["sources"]]
    }))
```

Pairs with a simple HTML/JS frontend that connects to SignalR and renders answers as they push.
This is how you build a real-time web chat UI without polling.

---

## Managed Identity — No API Keys

The gold standard for Azure-to-Azure auth. No secrets to store, rotate, or accidentally leak.

### Setup

1. Enable system-assigned Managed Identity on the Function App
2. Grant the Function App's identity access to needed resources:
   - **Azure Key Vault**: `Key Vault Secrets User` role (to read the go-omnivore-rag API key)
   - **Azure Blob Storage**: `Storage Blob Data Reader` role
   - **Azure Service Bus**: `Azure Service Bus Data Sender/Receiver` role
3. In Function code, use `DefaultAzureCredential` — it automatically uses the managed identity
   when running in Azure, falls back to local dev credentials (VS Code, CLI) locally

```python
from azure.identity import DefaultAzureCredential
from azure.keyvault.secrets import SecretClient

credential = DefaultAzureCredential()
kv_client = SecretClient(vault_url=KV_URL, credential=credential)
api_key = kv_client.get_secret("omnivore-rag-api-key").value
```

**For go-omnivore-rag itself:** Use `azidentity.NewDefaultAzureCredential()` to authenticate to
Azure Blob Storage (already done via SDK), Azure AI Search, and Azure OpenAI — eliminating the
`AZURE_SEARCH_API_KEY` and `AZURE_OPENAI_API_KEY` env vars when running in Azure.

---

## Go Custom Handlers

Azure Functions supports Go via custom handlers: the Functions host sends HTTP requests to a
locally running Go HTTP server inside the same process group.

**When to use Go for Functions:**
- You want to share types/logic with go-omnivore-rag (same module)
- Team is Go-only and doesn't want to maintain Python/JS Functions
- You need the performance characteristics of Go (rare for Functions)

**When not to:**
- Python and JavaScript have first-class support with richer SDK tooling
- Go custom handler debugging is less ergonomic
- Microsoft only supports the host startup/communication, not your Go code

**Minimal Go custom handler:**

```go
// functions/main.go
package main

import (
    "encoding/json"
    "net/http"
    "os"
)

func blobIngestHandler(w http.ResponseWriter, r *http.Request) {
    var invocation struct {
        Data struct {
            BlobTrigger string `json:"blobTrigger"`
        } `json:"Data"`
    }
    json.NewDecoder(r.Body).Decode(&invocation)

    // call go-omnivore-rag
    triggerIngest(invocation.Data.BlobTrigger)

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"Outputs": "{}", "Logs": []string{"done"}})
}

func main() {
    port := os.Getenv("FUNCTIONS_CUSTOMHANDLER_PORT")
    http.HandleFunc("/BlobIngestTrigger", blobIngestHandler)
    http.ListenAndServe(":"+port, nil)
}
```

`host.json` sets `"customHandler": {"description": {"defaultExecutablePath": "functions"}}`.

---

## Application Insights Integration

All Function Apps should connect to the same Application Insights instance as go-omnivore-rag
(or a separate one in the same workspace). This gives a single pane of glass.

### What Gets Captured Automatically

- Function invocation count, duration, success/failure rate
- Dependency calls (outbound HTTP to go-omnivore-rag)
- Exceptions with stack traces
- Custom telemetry (log.warning, log.error)

### Correlation: Trace a Request End-to-End

Add a `x-correlation-id` header when Functions call go-omnivore-rag:

```python
import uuid
correlation_id = str(uuid.uuid4())
headers = {
    "Authorization": f"Bearer {API_KEY}",
    "x-correlation-id": correlation_id
}
```

go-omnivore-rag logs the header. Application Insights links the Function invocation log and the
Go server log via the same correlation ID. One ID traces the full request across both services.

### Key Metrics to Alert On

| Metric | Condition | Alert |
|--------|-----------|-------|
| Function invocation failures | > 5 in 10 min | PagerDuty / email |
| Ingest job queue depth | > 50 messages | Slack warning |
| go-omnivore-rag HTTP 500s | Any 500 from dependency calls | Immediate alert |
| DLQ message count | > 0 | Email to ops team |
| Function cold start duration | P95 > 10s | Investigate plan |

---

## Full Architecture: Everything Connected

```
                         ┌─────────────────────────────────┐
                         │      Azure Blob Storage          │
                         │  Container: banner-release-notes │
                         └────────────┬────────────────────┘
                                      │ BlobCreated event
                                      ▼
                         ┌─────────────────────────────────┐
                         │         Azure Event Grid         │
                         └──────┬─────────────┬────────────┘
                                │             │
              ┌─────────────────▼──┐   ┌──────▼──────────────────┐
              │  Function:         │   │  Function:               │
              │  BlobIngestTrigger │   │  DocumentRegistryUpdate  │
              │  (calls /ingest)   │   │  (writes to Cosmos DB)   │
              └──────┬─────────────┘   └──────────────────────────┘
                     │
                     │ Service Bus message: "document-ingested"
                     ▼
          ┌─────────────────────────┐
          │   Service Bus Topic     │
          │   "document-ingested"   │
          └──┬──────────┬───────────┘
             │          │
  ┌──────────▼──┐  ┌────▼──────────────────┐
  │  Function:  │  │  Function:             │
  │  Notifier   │  │  UpgradeAnalysisStart  │
  │  (Teams/    │  │  (Durable orchestrator)│
  │   Slack)    │  └────┬───────────────────┘
  └─────────────┘       │ Fan-out
                 ┌──────┴──┬──────────┬──────────┐
                 ▼         ▼          ▼          ▼
           Changes    Breaking    Actions    Compat
          (/summarize/* — all call go-omnivore-rag)
                 └──────┬──┴──────────┴──────────┘
                        │ Fan-in
                        ▼
                   BuildReport
                        │
                        ▼
                  Cosmos DB Output   SignalR Output
               (persist report)   (push to web UI)

Timer Triggers (independent):
  ├── 02:00 daily → /banner/blob/sync
  ├── 08:00 Monday → weekly report orchestrator
  └── */30 min → health check → alert if unhealthy

HTTP Trigger (API proxy):
  External clients → Function proxy → go-omnivore-rag (private VNet)
```

---

## Implementation Priority

**Start here (highest ROI, low complexity):**

1. **Timer: daily blob sync** — cron job replacing `POST /banner/blob/sync` manual call.
   30 minutes to implement. Immediate value.

2. **Blob trigger: auto-ingest on upload** — eliminates the single biggest manual step.
   Requires Docker deploy of go-omnivore-rag to Azure Container Apps first.

3. **Durable Functions: parallel summarize** — `POST /banner/summarize/full` becomes 4x faster
   via fan-out. Medium complexity, high value for upgrade planning workflows.

**Next (medium complexity, high value):**

4. **Service Bus queue: async ingestion** — add reliability (retry, DLQ) to the ingest pipeline.
   Required before ingesting large document batches unattended.

5. **Timer: index health monitor** — operational hygiene. Know before users do if the index is broken.

6. **HTTP proxy Function** — add auth layer without changing Go code. Needed before any
   external-facing deployment.

**Later (infrastructure investment required):**

7. **Cosmos DB + change feed** — adds document registry and Q&A history. Valuable but needs
   schema design work first.

8. **SignalR output: real-time web UI** — requires a frontend. The backend Function piece is
   simple; the frontend is the work.

9. **Human approval Durable workflow** — for governed environments where index recreation needs
   sign-off. Niche but important for regulated IT shops.

---

## What Needs to Be Built in go-omnivore-rag First

| Blocker | What to Add | Where |
|---------|-------------|-------|
| No auth | API key middleware | `internal/api/router.go` |
| `localhost` only | Dockerfile + Container Apps deployment | New `Dockerfile` |
| No correlation ID | `x-correlation-id` header logging | `internal/api/router.go` |
| No structured logs | Replace `log.Printf` with `slog` (JSON output) | All packages |
| No confidence in response | Add avg `score` field to `AskResponse` | `internal/rag/rag.go` |
| Ingest is synchronous | Optional: accept ingest job and return 202 Accepted + job ID | `internal/api/handlers.go` |

The auth + Docker items are blocking for any Azure Functions integration.
Everything else improves reliability and observability but isn't strictly required to start.
