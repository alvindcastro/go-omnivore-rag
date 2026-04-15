# System Reliability & Observability

Brainstorm for making go-omnivore-rag production-grade — from visibility into what it's doing
to graceful handling of failures. This expands on the brief Observability section in UPGRADES.md.

---

## Table of Contents

1. [Current State](#current-state)
2. [Structured Logging](#structured-logging)
3. [Metrics & Cost Tracking](#metrics--cost-tracking)
4. [Distributed Tracing](#distributed-tracing)
5. [Health & Readiness Endpoints](#health--readiness-endpoints)
6. [Alerting & SLOs](#alerting--slos)
7. [Resilience Patterns](#resilience-patterns)
8. [Graceful Shutdown](#graceful-shutdown)
9. [Index Health Monitoring](#index-health-monitoring)
10. [Chaos & Load Testing](#chaos--load-testing)
11. [Priority Matrix](#priority-matrix)

---

## Current State

| Area | Status | Gap |
|------|--------|-----|
| Logging | `log.Printf` (unstructured) | Not machine-parseable, no correlation IDs |
| Metrics | None | No visibility into query latency, token cost, error rates |
| Tracing | None | Can't see where time is spent within a RAG call |
| Health check | `GET /health` returns `"ok"` | No dependency checks (Azure Search, OpenAI reachability) |
| Alerting | None | Silent failures go undetected |
| Retry logic | Ad-hoc per Azure client | Inconsistent, no circuit breakers |
| Rate limiting | Ingest-side sleep only | No server-side request limiting |
| Graceful shutdown | Not implemented | In-flight requests dropped on Ctrl-C |

---

## Structured Logging

### Why `slog` (stdlib, no new deps)

The standard library `log/slog` package (Go 1.21+) produces JSON-structured output that Azure
Monitor, Datadog, Grafana Loki, and any log aggregator can parse.

```go
// cmd/main.go — set once, use everywhere
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))
slog.SetDefault(logger)
```

Every log line becomes queryable:
```json
{
  "time": "2026-04-06T12:00:00Z",
  "level": "INFO",
  "msg": "banner ask complete",
  "request_id": "abc-123",
  "mode": "auto",
  "route_decision": "local",
  "chunks_retrieved": 5,
  "top_score": 0.042,
  "duration_ms": 1823,
  "prompt_tokens": 1240,
  "completion_tokens": 310
}
```

### Log field conventions

| Field | Type | Description |
|-------|------|-------------|
| `request_id` | string | From `X-Request-ID` middleware — ties all log lines for one request together |
| `endpoint` | string | e.g. `"banner_ask"`, `"sop_ingest"` |
| `mode` | string | `"local"`, `"web"`, `"hybrid"`, `"auto"` |
| `route_decision` | string | Which mode auto-routing selected |
| `chunks_retrieved` | int | Number of search results used |
| `top_score` | float | Highest similarity score from Azure Search |
| `duration_ms` | int | End-to-end handler time |
| `prompt_tokens` | int | From Azure OpenAI usage object |
| `completion_tokens` | int | From Azure OpenAI usage object |
| `source_type` | string | `"banner"`, `"sop"`, `"banner_user_guide"` |
| `error` | string | Error message (error-level logs only) |
| `azure_status` | int | HTTP status from an Azure client call |

### Log levels

- `INFO` — every inbound request (start + complete), scheduled jobs
- `WARN` — retries triggered, soft rate-limit hits, low-confidence auto-routing decisions
- `ERROR` — Azure calls that exhaust retries, malformed requests, startup config failures
- `DEBUG` — individual chunk IDs indexed, per-query Azure Search parameters (verbose, off by default)

---

## Metrics & Cost Tracking

### What to measure

The three pillars for this service: **latency**, **error rate**, and **cost**.

#### Latency

RAG calls have multiple phases. Track each independently so you can attribute spikes:

| Metric | What it captures |
|--------|-----------------|
| `rag.embed.duration_ms` | Time to embed the question (Azure OpenAI ada-002) |
| `rag.search.duration_ms` | Time for Azure AI Search hybrid query |
| `rag.chat.duration_ms` | Time for GPT-4o-mini chat completion |
| `rag.ask.duration_ms` | Total end-to-end RAG latency |
| `ingest.embed.duration_ms` | Per-chunk embedding time during ingestion |

#### Error rates

| Metric | Alert threshold idea |
|--------|---------------------|
| `azure.openai.429_rate` | > 5% in 5 min → TPM limit being hit |
| `azure.search.error_rate` | Any non-zero → index/key misconfiguration |
| `api.5xx_rate` | > 1% → investigate immediately |
| `ingest.failed_chunks` | > 0 → document quality or Azure issue |

#### Token cost

Azure OpenAI returns usage on every call. Aggregate daily:

| Metric | Use |
|--------|-----|
| `openai.prompt_tokens` | Charged at input rate |
| `openai.completion_tokens` | Charged at output rate |
| `openai.embed_tokens` | Charged per-token for ada-002 |

At gpt-4o-mini pricing (~$0.00015/1K input, $0.00060/1K output), a typical ask costs $0.001–0.003.
Log it per request; aggregate weekly to build a cost-per-user or cost-per-endpoint view.

### Implementation options

**Option A: Prometheus + Grafana (self-hosted, no new Azure cost)**

```go
// go get github.com/prometheus/client_golang
var askDuration = prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name:    "rag_ask_duration_ms",
        Buckets: []float64{200, 500, 1000, 2000, 4000, 8000},
    },
    []string{"mode", "source_type"},
)

// in the handler
timer := prometheus.NewTimer(prometheus.ObserverFunc(func(v float64) {
    askDuration.WithLabelValues(mode, sourceType).Observe(v * 1000)
}))
defer timer.ObserveDuration()
```

Expose at `GET /metrics` for Prometheus scraping. Run Grafana alongside in Docker Compose.

**Option B: Azure Monitor custom metrics (zero new infra if already on Azure)**

```go
// POST to Azure Monitor Data Collection Endpoint
// or use the Azure Monitor SDK
```

Advantage: native integration with Azure Alerts, dashboards in the Azure portal.

**Option C: Datadog / Grafana Cloud (SaaS, easiest ops)**

Both have Go client libraries. Zero infra to run. Cost is per host/metrics volume.

---

## Distributed Tracing

### Why it matters here

A single `/banner/ask` call touches 3 external Azure services sequentially. When latency spikes,
you need to know: was it the embedding call, the search call, or the chat completion?

Without tracing, the only signal is total request duration in logs. With tracing, you get a
waterfall diagram showing each span.

### OpenTelemetry (vendor-neutral)

```go
// go get go.opentelemetry.io/otel
// go get go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin

// cmd/main.go — initialize once
tp := initTracerProvider() // export to OTLP endpoint
otel.SetTracerProvider(tp)

// internal/api/router.go — auto-instrument all HTTP handlers
r.Use(otelgin.Middleware("go-omnivore-rag"))
```

Manual spans in the RAG pipeline:

```go
tracer := otel.Tracer("go-omnivore-rag")

func (p *Pipeline) askWithPrompt(ctx context.Context, req AskRequest) (AskResponse, error) {
    ctx, embedSpan := tracer.Start(ctx, "azure.openai.embed")
    embedding, err := p.openai.EmbedText(ctx, req.Question)
    embedSpan.End()

    ctx, searchSpan := tracer.Start(ctx, "azure.search.hybrid")
    searchSpan.SetAttributes(
        attribute.Int("top_k", req.TopK),
        attribute.String("source_type", req.SourceType),
    )
    results, err := p.search.HybridSearch(ctx, embedding, req)
    searchSpan.End()

    ctx, chatSpan := tracer.Start(ctx, "azure.openai.chat")
    answer, err := p.openai.ChatComplete(ctx, systemPrompt, userPrompt)
    chatSpan.End()
    // ...
}
```

### Trace export targets

| Backend | How | Best for |
|---------|-----|---------|
| Azure Monitor (Application Insights) | OTLP exporter → App Insights | Already on Azure |
| Jaeger (self-hosted) | OTLP exporter → Jaeger collector | Local dev, Docker Compose |
| Grafana Tempo | OTLP exporter → Tempo | If using Grafana stack |
| Datadog APM | Datadog OTLP receiver | Existing Datadog users |

All accept the same OTLP protocol — change the exporter endpoint, not the instrumentation code.

### Trace correlation with logs

Inject the trace ID into every `slog` log line so you can pivot from a log line to a trace:

```go
// In middleware or handler, after span is created
span := trace.SpanFromContext(ctx)
slog.InfoContext(ctx, "banner ask complete",
    "trace_id", span.SpanContext().TraceID().String(),
    "span_id",  span.SpanContext().SpanID().String(),
    // ...other fields
)
```

Azure Monitor, Datadog, and Grafana all support trace-to-log linking when IDs match.

---

## Health & Readiness Endpoints

### Current `/health`

Returns `{"status": "ok"}` with HTTP 200. This tells a load balancer the process is alive but
says nothing about whether it can actually serve requests.

### Recommended: two endpoints

**`GET /health/live`** — liveness: is the process alive?
- Returns 200 always (if it can respond, it's alive)
- Kubernetes uses this to decide whether to restart the container
- Does NOT check external dependencies (a broken Azure Search shouldn't trigger a restart)

**`GET /health/ready`** — readiness: can this instance serve traffic?
- Checks that required dependencies are reachable
- Returns 503 if any required dependency is down (Azure OpenAI, Azure Search)
- Kubernetes uses this to decide whether to route traffic to this instance
- Load balancers use this to drain unhealthy instances

```go
// internal/api/handlers.go
func (h *Handler) Readiness(c *gin.Context) {
    checks := map[string]string{}
    healthy := true

    // Check Azure Search
    if err := h.search.Ping(c.Request.Context()); err != nil {
        checks["azure_search"] = "unreachable: " + err.Error()
        healthy = false
    } else {
        checks["azure_search"] = "ok"
    }

    // Check Azure OpenAI (lightweight: check the models endpoint, not a real embedding)
    if err := h.openai.Ping(c.Request.Context()); err != nil {
        checks["azure_openai"] = "unreachable: " + err.Error()
        healthy = false
    } else {
        checks["azure_openai"] = "ok"
    }

    status := http.StatusOK
    if !healthy {
        status = http.StatusServiceUnavailable
    }
    c.JSON(status, gin.H{"status": checks})
}
```

### What `Ping` looks like for Azure clients

For Azure Search: `GET /indexes?$top=0` — cheap, just checks auth + connectivity.
For Azure OpenAI: `GET /openai/deployments` — returns deployment list, verifies key + endpoint.

---

## Alerting & SLOs

### Defining SLOs for this service

SLOs (Service Level Objectives) define what "good" looks like. Define them before setting alerts.

| SLO | Target | Rationale |
|-----|--------|-----------|
| Ask endpoint availability | 99.5% | Allowed ~22 minutes downtime/month |
| Ask p95 latency | < 5 seconds | RAG is slow by nature; 5s is the UX threshold |
| Ingest success rate | > 99% of chunks indexed | Occasional Azure 429 retries are acceptable |
| Index freshness | Indexed within 24h of document upload | Prevents stale knowledge base |

### Alert rules (Azure Monitor or Grafana)

```
ALERT: High 5xx rate
  condition: rate(api.5xx) > 1% for 5 minutes
  severity: P2
  channel: Teams #banner-ops

ALERT: Azure OpenAI 429 storm
  condition: rate(azure.openai.429) > 10% for 2 minutes
  severity: P2
  action: page on-call, link runbook

ALERT: Index chunk count drop
  condition: index.chunk_count < (previous_day * 0.95)
  severity: P3
  channel: Teams #banner-ops

ALERT: Ask latency p95 > 8s
  condition: p95(rag.ask.duration_ms) > 8000 for 10 minutes
  severity: P3
  channel: Teams #banner-ops

ALERT: Health check failing
  condition: /health/ready returns non-200 for 3 consecutive checks
  severity: P1
  action: page on-call immediately
```

### Runbooks

For each P1/P2 alert, a linked runbook doc reduces MTTR:
- What the alert means
- First 3 things to check
- How to mitigate (increase TPM limit, restart container, etc.)
- Escalation path

---

## Resilience Patterns

### Circuit Breaker for Azure calls

The current retry logic is simple: wait N seconds, retry M times, then fail. A circuit breaker
is more nuanced: after N consecutive failures, stop calling the service entirely for a cooldown
period (prevents hammering a down service and consuming quota).

```
CLOSED → calls flow normally
   ↓ (N consecutive failures)
OPEN → calls fail fast for T seconds (no Azure calls made)
   ↓ (after T seconds)
HALF-OPEN → allow one probe call
   ↓ success          ↓ failure
CLOSED again       OPEN again
```

Libraries: `github.com/sony/gobreaker` (popular, zero deps), `github.com/afex/hystrix-go`.

Candidate circuit breakers:
- Azure OpenAI embedding calls (most likely to rate-limit)
- Azure OpenAI chat completion calls
- Azure Search calls (less likely to fail, but index corruption is possible)

### Timeout hierarchy

Each external call should have an explicit deadline, not rely on the default (which is often none):

| Call | Suggested timeout |
|------|------------------|
| Embedding (ada-002) | 10 seconds |
| Chat completion (gpt-4o-mini) | 30 seconds |
| Azure Search query | 10 seconds |
| Azure Blob list/download | 30 seconds |
| Health check dependencies | 3 seconds |

```go
// Propagate context with timeout from the request
ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
defer cancel()
result, err := p.openai.ChatComplete(ctx, systemPrompt, userPrompt)
```

The Gin request context is already deadline-aware if the client disconnects — propagating it
through the call stack means Azure calls are automatically cancelled when the client hangs up.

### Retry policy (current vs. recommended)

Current: ad-hoc sleep-and-retry per Azure client, no backoff consistency.

Recommended: extract a shared retry helper with exponential backoff + jitter:

```go
// internal/azure/retry.go
func WithRetry(ctx context.Context, maxAttempts int, fn func() error) error {
    var err error
    for attempt := 0; attempt < maxAttempts; attempt++ {
        err = fn()
        if err == nil {
            return nil
        }
        if !isRetryable(err) { // only 429 and 503 are retryable
            return err
        }
        backoff := time.Duration(math.Pow(2, float64(attempt))) * 500 * time.Millisecond
        jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
        select {
        case <-time.After(backoff + jitter):
        case <-ctx.Done():
            return ctx.Err()
        }
    }
    return err
}
```

---

## Graceful Shutdown

Currently `Ctrl-C` or a container stop signal kills the process immediately. Any in-flight RAG
request (which can take 3–8 seconds) is dropped mid-response.

### What graceful shutdown looks like

1. Receive `SIGTERM` (Kubernetes, ACA, or `Ctrl-C`)
2. Stop accepting new connections
3. Wait for in-flight requests to complete (up to a drain timeout, e.g. 30s)
4. Close database/cache connections, flush telemetry
5. Exit cleanly

```go
// cmd/main.go
srv := &http.Server{
    Addr:    ":" + cfg.Port,
    Handler: router,
}

go func() {
    if err := srv.ListenAndServe(); err != http.ErrServerClosed {
        log.Fatal(err)
    }
}()

// Wait for interrupt signal
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

slog.Info("shutting down, draining in-flight requests...")
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := srv.Shutdown(ctx); err != nil {
    slog.Error("forced shutdown", "error", err)
}

// Flush telemetry, close DB connections, etc.
tp.Shutdown(ctx) // flush OpenTelemetry spans
slog.Info("shutdown complete")
```

ACA and Kubernetes send `SIGTERM` 30 seconds before `SIGKILL` — use that window.

---

## Index Health Monitoring

The Azure AI Search index is the most fragile component — a `overwrite: true` ingest wipes it,
and there's no automatic backup.

### What to monitor

| Check | How often | Alert if |
|-------|-----------|---------|
| Total chunk count | Every 30 min | Drops more than 5% from baseline |
| Last ingest timestamp | Every hour | Older than 7 days (stale knowledge) |
| Search latency (p95) | Every 15 min | > 500ms (index may be degraded) |
| Index availability | Every 5 min | Any search failure |
| Source type distribution | Daily | `banner` or `sop` counts drop to zero |

### Index snapshot / backup

Azure AI Search doesn't support point-in-time snapshots. A lightweight alternative:

1. After every successful ingest, export a JSON manifest: list of ingested file paths, chunk counts,
   and MD5 hashes of source files
2. Store the manifest in Azure Blob Storage with a timestamp
3. If the index is accidentally wiped, use the manifest to identify what needs re-ingesting
4. For a full backup: periodically export all index documents via Azure Search scroll API and
   store as compressed NDJSON in Blob Storage

This isn't instant recovery, but it eliminates the "we don't know what was in the index" problem.

---

## Chaos & Load Testing

### Why bother for an internal tool?

Even internal tools fail ungracefully if an Azure service has an outage or quota is exceeded.
Testing failure modes deliberately is cheaper than discovering them during a Banner upgrade crunch.

### Failure scenarios to test manually

| Scenario | How to simulate | Expected behavior |
|----------|----------------|-------------------|
| Azure OpenAI rate limit | Lower TPM limit in portal during test | Retry with backoff, 429 logged |
| Azure Search unavailable | Wrong endpoint in `.env` | `GET /health/ready` returns 503 |
| Azure OpenAI unavailable | Wrong key in `.env` | Embedding fails, 500 returned, not a hang |
| Large document ingest | 500-page PDF | No memory spike, progress logged, completes |
| Concurrent ask requests | `ab -n 100 -c 10 /banner/ask` | No goroutine leak, stable latency |
| Container killed mid-ingest | `docker kill` during ingest | Index partially populated, no corruption |

### Load testing

`k6` or `hey` for simple load tests against the ask endpoints:

```bash
# go install github.com/rakyll/hey@latest
hey -n 50 -c 5 -m POST -H "Content-Type: application/json" \
    -d '{"question":"What changed in Banner General 9.3.37?","top_k":5}' \
    http://localhost:8000/banner/ask
```

Key things to watch: p95 latency under load, goroutine count stability, Azure quota consumption.

---

## Priority Matrix

| Improvement | Impact | Effort | Priority |
|-------------|--------|--------|----------|
| Structured logging (`slog`) | High — enables all other observability | Low | **Do first** |
| Request ID middleware | Medium — required for log correlation | Low | **Do first** |
| Graceful shutdown | High — prevents dropped requests in ACA/K8s | Low | **Do first** |
| `/health/ready` with dep checks | High — load balancer & k8s integration | Low | **Do first** |
| Context propagation + timeouts | High — prevents hung goroutines | Medium | Do soon |
| Prometheus metrics + Grafana | High — visibility into latency & errors | Medium | Do soon |
| Circuit breakers (Azure calls) | Medium — prevents quota storms | Medium | Next sprint |
| OpenTelemetry tracing | High — root-cause latency spikes | High | Next sprint |
| Retry policy consolidation | Medium — consistency across Azure clients | Medium | Next sprint |
| Azure Monitor custom metrics | Medium — if already on Azure | Low | Next sprint |
| Index health monitoring | Medium — catches stale/wiped index early | Medium | Later |
| Index snapshot/backup | High — disaster recovery | Medium | Later |
| SLO dashboards + alerting | Medium — requires metrics first | High | Later |
| Load testing suite | Low — educational codebase context | Medium | Later |
