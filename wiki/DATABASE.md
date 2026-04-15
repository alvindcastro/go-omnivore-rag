# AI-Ready SQL & Database Evolution

Brainstorm for moving beyond Azure AI Search — adding a relational layer and/or migrating vector
storage to PostgreSQL (pgvector). Covers motivation, architecture patterns, migration path, and
trade-offs.

---

## Table of Contents

1. [Current Architecture & What's Missing](#current-architecture--whats-missing)
2. [Why Add a Relational Layer?](#why-add-a-relational-layer)
3. [PostgreSQL + pgvector](#postgresql--pgvector)
4. [AI-Ready SQL Patterns](#ai-ready-sql-patterns)
5. [What Stays, What Moves](#what-stays-what-moves)
6. [Migration Path: Azure Search → pgvector](#migration-path-azure-search--pgvector)
7. [Schema Design](#schema-design)
8. [Query Patterns (SQL + Vector)](#query-patterns-sql--vector)
9. [Hybrid Architecture: Both Backends](#hybrid-architecture-both-backends)
10. [Other Database Options](#other-database-options)
11. [Go Database Layer](#go-database-layer)
12. [Trade-off Summary](#trade-off-summary)
13. [Priority & Next Steps](#priority--next-steps)

---

## Current Architecture & What's Missing

The service currently has **no relational database**. All persistent state lives in two places:

| What | Where | Problem |
|------|-------|---------|
| Document chunks + embeddings | Azure AI Search | Vendor lock-in, cost, limited SQL querying |
| Job state (ingest progress) | In-memory only | Lost on restart, no history |
| User feedback (thumbs up/down) | Not stored | No feedback loop |
| Cache (semantic) | Not implemented | Would need external store |
| Conversation history | Not stored | No multi-turn memory |
| Audit log (who asked what) | Not stored | No compliance trail |
| Ingest manifests | Not stored | Can't tell what's in the index |

A relational database would fix all of the non-vector items. pgvector would fix the vector item too.

---

## Why Add a Relational Layer?

### Things SQL is better at than Azure AI Search

| Use case | Azure AI Search | PostgreSQL |
|----------|----------------|-----------|
| Filter by metadata (module, version, year) | OData filters (limited) | Full SQL WHERE — joins, subqueries, window functions |
| Store job state | Not its job | Native: `INSERT INTO jobs ...` |
| Store feedback | Not its job | Native, with FK to ask_log |
| Aggregate stats (cost per user, queries per day) | Not supported | `GROUP BY`, `COUNT`, `SUM` |
| Full-text search | Good (BM25) | Good with `tsvector` + `tsquery` |
| Vector search | Good (HNSW) | Good with pgvector HNSW |
| Joins across data types | Not possible | Native |
| Transactions | Not supported | Native ACID |
| Backup / restore | Azure-managed, limited | Standard pg_dump / PITR |
| Portability | Azure-only | Runs anywhere (local, Docker, Fly.io, RDS, Azure DB) |

### The key insight

Right now metadata filtering (module, version, year) is done with OData syntax passed to Azure
Search. This works but is limited — you can't do "give me all documents where version is between
9.3.30 and 9.3.38" or "find modules that have more than 50 chunks". With a relational layer,
these become simple SQL queries.

---

## PostgreSQL + pgvector

### What pgvector is

`pgvector` is a PostgreSQL extension that adds:
- A `vector(N)` column type for storing float32 arrays (e.g., 1536-dimensional embeddings)
- `<->` (L2), `<=>` (cosine), `<#>` (inner product) distance operators
- HNSW and IVFFlat indexes for approximate nearest-neighbor search
- Exact KNN search for small datasets

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE chunks (
    id          TEXT PRIMARY KEY,
    chunk_text  TEXT NOT NULL,
    embedding   vector(1536),        -- text-embedding-ada-002 output
    source_path TEXT,
    page_number INT,
    chunk_index INT,
    source_type TEXT,                -- 'banner', 'sop', 'banner_user_guide'
    banner_module   TEXT,
    banner_version  TEXT,
    year        TEXT,
    ingested_at TIMESTAMPTZ DEFAULT NOW()
);

-- HNSW index for cosine similarity (same algorithm Azure Search uses)
CREATE INDEX ON chunks USING hnsw (embedding vector_cosine_ops)
WITH (m = 16, ef_construction = 64);
```

### Vector search query

```sql
-- Find top-5 chunks most similar to a query embedding, filtered by module and version
SELECT
    id,
    chunk_text,
    source_path,
    page_number,
    banner_module,
    banner_version,
    1 - (embedding <=> $1::vector) AS similarity_score
FROM chunks
WHERE
    source_type = 'banner'
    AND banner_module = 'Finance'
    AND banner_version LIKE '9.3.3%'
ORDER BY embedding <=> $1::vector
LIMIT 5;
```

This is equivalent to Azure Search's hybrid query but in plain SQL. The metadata filters are
standard SQL WHERE clauses — composable, debuggable, and testable without an Azure subscription.

### Hybrid search in SQL

BM25 full-text + vector similarity combined:

```sql
-- Hybrid: weighted combination of BM25 rank and vector similarity
WITH
vector_results AS (
    SELECT id,
           1 - (embedding <=> $1::vector) AS vec_score,
           ROW_NUMBER() OVER (ORDER BY embedding <=> $1::vector) AS vec_rank
    FROM chunks
    WHERE source_type = $2
    LIMIT 20
),
text_results AS (
    SELECT id,
           ts_rank(to_tsvector('english', chunk_text), plainto_tsquery('english', $3)) AS text_score,
           ROW_NUMBER() OVER (ORDER BY ts_rank(to_tsvector('english', chunk_text), plainto_tsquery('english', $3)) DESC) AS text_rank
    FROM chunks
    WHERE source_type = $2
      AND to_tsvector('english', chunk_text) @@ plainto_tsquery('english', $3)
    LIMIT 20
),
-- Reciprocal Rank Fusion (RRF) — standard hybrid ranking formula
rrf AS (
    SELECT
        COALESCE(v.id, t.id) AS id,
        (COALESCE(1.0 / (60 + v.vec_rank), 0) + COALESCE(1.0 / (60 + t.text_rank), 0)) AS rrf_score
    FROM vector_results v
    FULL OUTER JOIN text_results t USING (id)
)
SELECT c.id, c.chunk_text, c.source_path, c.page_number, c.banner_module, c.banner_version, r.rrf_score
FROM rrf r
JOIN chunks c USING (id)
ORDER BY r.rrf_score DESC
LIMIT $4;
```

This is the same Reciprocal Rank Fusion algorithm that Azure AI Search uses internally for hybrid
search, now running in plain PostgreSQL with zero Azure dependency.

---

## AI-Ready SQL Patterns

These patterns describe how to structure a SQL schema so LLMs and RAG pipelines can work with it
effectively — not just for this project, but as general patterns.

### Pattern 1: Metadata-rich chunk table

Store all metadata as typed columns, not as a flat JSON blob. This enables:
- Filtering in SQL without JSON path queries
- Indexing individual metadata fields
- Aggregations and analytics
- LLMs can describe the schema and generate accurate SQL queries

```sql
-- Rich metadata = LLM-friendly schema
CREATE TABLE chunks (
    id              TEXT PRIMARY KEY,
    chunk_text      TEXT NOT NULL,
    embedding       vector(1536),
    -- Source provenance
    source_path     TEXT NOT NULL,
    source_type     TEXT NOT NULL CHECK (source_type IN ('banner', 'sop', 'banner_user_guide')),
    filename        TEXT,
    -- Document structure
    page_number     INT,
    chunk_index     INT,
    section_heading TEXT,         -- from SOP breadcrumbs; NULL for PDFs
    -- Banner-specific metadata
    banner_module   TEXT,
    banner_version  TEXT,
    year            TEXT,
    -- Lifecycle
    ingested_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    content_hash    TEXT         -- MD5 of chunk_text, for change detection
);
```

### Pattern 2: Ask log for analytics and feedback

Store every RAG call. This enables: cost tracking, popular question analysis, feedback linking,
audit compliance.

```sql
CREATE TABLE ask_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asked_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    question        TEXT NOT NULL,
    answer          TEXT NOT NULL,
    mode_requested  TEXT,           -- 'local', 'web', 'hybrid', 'auto'
    mode_used       TEXT,           -- what auto-routing actually chose
    source_type     TEXT,
    top_score       FLOAT,
    chunks_retrieved INT,
    prompt_tokens   INT,
    completion_tokens INT,
    duration_ms     INT,
    request_id      TEXT,
    api_key_hint    TEXT            -- first 8 chars of API key, for per-client attribution
);

-- Useful queries on this table:
-- SELECT DATE(asked_at), COUNT(*), AVG(duration_ms) FROM ask_log GROUP BY 1 ORDER BY 1;
-- SELECT SUM(prompt_tokens + completion_tokens) * 0.00015 / 1000 AS estimated_cost FROM ask_log WHERE asked_at > NOW() - INTERVAL '7 days';
-- SELECT question, top_score FROM ask_log WHERE mode_used = 'web' ORDER BY asked_at DESC LIMIT 20;
```

### Pattern 3: Feedback table

Link thumbs up/down to specific ask log entries:

```sql
CREATE TABLE feedback (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ask_id      UUID NOT NULL REFERENCES ask_log(id),
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    helpful     BOOL NOT NULL,
    comment     TEXT,
    user_hint   TEXT           -- anonymized user identifier if available
);

-- Find questions that consistently get negative feedback:
-- SELECT a.question, COUNT(*) filter (WHERE f.helpful = false) AS thumbs_down
-- FROM feedback f JOIN ask_log a ON a.id = f.ask_id
-- GROUP BY a.question ORDER BY thumbs_down DESC;
```

### Pattern 4: Job state table

Replace in-memory job store with durable state:

```sql
CREATE TABLE ingest_jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'done', 'failed')),
    source_type TEXT,           -- 'banner', 'sop'
    docs_path   TEXT,
    total_files INT,
    total_chunks INT,
    indexed_chunks INT DEFAULT 0,
    error_message TEXT,
    triggered_by TEXT           -- 'api', 'cron', 'n8n'
);
```

### Pattern 5: Text-to-SQL readiness

A well-designed schema lets an LLM generate accurate SQL queries over your data without hallucinating
column names. Principles:

1. **Descriptive column names** — `banner_version` not `bv`; `source_type` not `type`
2. **Consistent naming** — snake_case throughout; timestamps always end in `_at`
3. **Enum constraints** — `CHECK (source_type IN (...))` documents valid values in the schema itself
4. **No JSON blobs for filterable data** — if an LLM needs to filter by it, make it a column
5. **Schema comments** — `COMMENT ON COLUMN chunks.top_score IS 'Highest cosine similarity score...'`
6. **Views for common queries** — `CREATE VIEW recent_asks AS SELECT * FROM ask_log WHERE asked_at > NOW() - INTERVAL '7 days'`

An LLM given `\d+ chunks` from `psql` can then accurately write queries like:
```
"Show me all chunks from Banner Finance version 9.3.37 that mention prerequisites"
→ SELECT chunk_text, page_number FROM chunks WHERE banner_module = 'Finance' AND banner_version = '9.3.37' AND chunk_text ILIKE '%prerequisite%';
```

---

## What Stays, What Moves

If we add PostgreSQL + pgvector, not everything needs to move:

| Component | Keep in Azure Search | Move to PostgreSQL | Rationale |
|-----------|---------------------|-------------------|-----------|
| Vector embeddings + chunks | Option A | Option B (pgvector) | pgvector = portable, cheaper; Azure Search = managed, higher throughput |
| Hybrid search | Option A | Option B (RRF in SQL) | Azure Search has semantic reranking; SQL RRF is simpler |
| Job state | ✗ | ✓ Always | SQL is the right tool |
| Ask log | ✗ | ✓ Always | SQL is the right tool |
| Feedback | ✗ | ✓ Always | SQL is the right tool |
| Semantic cache | ✗ | ✓ (pgvector) | vector similarity lookup in same DB |
| Conversation history | ✗ | ✓ Always | SQL is the right tool |

**Pragmatic recommendation:** Start by adding PostgreSQL only for operational data (jobs, ask_log,
feedback, cache). Keep Azure Search for vector/hybrid search — it's already working and well-tuned.
Migrate chunks to pgvector only if you want to eliminate the Azure dependency or cut costs.

---

## Migration Path: Azure Search → pgvector

This is a full migration, not required immediately. Document it as a future option.

### Phase 1: Add PostgreSQL alongside Azure Search (parallel run)

1. Stand up PostgreSQL (Docker locally, Azure Database for PostgreSQL in production)
2. Install `pgvector` extension
3. Create the `chunks`, `ask_log`, `feedback`, `ingest_jobs` tables
4. Wire job state and ask logging to PostgreSQL — no behavior change, just durability
5. Run for 2–4 weeks to verify stability

### Phase 2: Dual-write chunks

During ingestion, write each chunk + embedding to both Azure Search and PostgreSQL:

```go
// internal/ingest/ingest.go — after current Azure Search upload
if err := search.UploadDocuments(batch); err != nil { ... }

// Also write to PostgreSQL
if err := store.InsertChunks(ctx, batch); err != nil {
    slog.Warn("pg chunk write failed, continuing with Azure Search", "error", err)
}
```

Both stores are now in sync. Zero production impact — Azure Search is still the query backend.

### Phase 3: Shadow query (validate pgvector results)

Add a flag `SHADOW_SEARCH=true` that runs the same query against both backends and logs
differences. Tune pgvector index parameters (`m`, `ef_construction`) until results match
Azure Search within acceptable variance.

```go
if cfg.ShadowSearch {
    pgResults, _ := store.HybridSearch(ctx, embedding, req)
    slog.Info("shadow search comparison",
        "azure_top_id", azureResults[0].ID,
        "pg_top_id", pgResults[0].ID,
        "match", azureResults[0].ID == pgResults[0].ID,
    )
}
```

### Phase 4: Route a percentage of traffic to pgvector

Use a feature flag (`VECTOR_BACKEND=azure|pg|shadow`) to gradually shift query traffic:

- `azure` — all queries use Azure Search (default)
- `pg` — all queries use pgvector
- `shadow` — use Azure Search, but run pgvector in background and log differences

Canary 10% → 50% → 100% while monitoring answer quality metrics.

### Phase 5: Decommission Azure Search

Once pgvector is fully validated:
1. Remove Azure Search client and index management endpoints
2. Remove `azure/search.go`
3. Delete the Azure Search resource
4. Update INTERNALS.md and deployment docs

---

## Schema Design

Full recommended schema for a pgvector-based deployment:

```sql
-- Enable extensions
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "pgcrypto";   -- gen_random_uuid()

-- ============================================================
-- CHUNK STORE (replaces Azure AI Search index)
-- ============================================================
CREATE TABLE chunks (
    id              TEXT PRIMARY KEY,            -- MD5(source_path|page|chunk_index)
    chunk_text      TEXT NOT NULL,
    embedding       vector(1536),                -- text-embedding-ada-002
    source_path     TEXT NOT NULL,
    source_type     TEXT NOT NULL,
    filename        TEXT,
    page_number     INT,
    chunk_index     INT,
    section_heading TEXT,
    banner_module   TEXT,
    banner_version  TEXT,
    year            TEXT,
    ingested_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    content_hash    TEXT
);

-- GIN index for full-text search (BM25 approximation)
CREATE INDEX chunks_fts_idx ON chunks USING gin(to_tsvector('english', chunk_text));

-- HNSW for cosine similarity (same as Azure Search default)
CREATE INDEX chunks_embedding_idx ON chunks USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- B-tree indexes for metadata filtering
CREATE INDEX chunks_source_type_idx ON chunks(source_type);
CREATE INDEX chunks_banner_module_idx ON chunks(banner_module);
CREATE INDEX chunks_banner_version_idx ON chunks(banner_version);

-- ============================================================
-- OPERATIONAL TABLES
-- ============================================================
CREATE TABLE ingest_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at     TIMESTAMPTZ,
    status          TEXT NOT NULL DEFAULT 'pending',
    source_type     TEXT,
    docs_path       TEXT,
    total_chunks    INT,
    indexed_chunks  INT DEFAULT 0,
    failed_chunks   INT DEFAULT 0,
    error_message   TEXT,
    triggered_by    TEXT
);

CREATE TABLE ask_log (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asked_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    question            TEXT NOT NULL,
    answer              TEXT NOT NULL,
    mode_requested      TEXT,
    mode_used           TEXT,
    source_type         TEXT,
    top_score           FLOAT,
    chunks_retrieved    INT,
    prompt_tokens       INT,
    completion_tokens   INT,
    duration_ms         INT,
    request_id          TEXT,
    api_key_hint        TEXT
);

CREATE TABLE feedback (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ask_id      UUID NOT NULL REFERENCES ask_log(id) ON DELETE CASCADE,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    helpful     BOOL NOT NULL,
    comment     TEXT
);

-- ============================================================
-- SEMANTIC CACHE
-- ============================================================
CREATE TABLE ask_cache (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question    TEXT NOT NULL,
    embedding   vector(1536) NOT NULL,
    answer      TEXT NOT NULL,
    sources_json JSONB,                       -- serialized []SourceChunk
    cached_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ,
    hit_count   INT DEFAULT 0
);

CREATE INDEX ask_cache_embedding_idx ON ask_cache USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- ============================================================
-- USEFUL VIEWS
-- ============================================================
CREATE VIEW recent_asks AS
    SELECT * FROM ask_log WHERE asked_at > NOW() - INTERVAL '7 days';

CREATE VIEW daily_cost_estimate AS
    SELECT
        DATE(asked_at) AS date,
        COUNT(*) AS total_asks,
        SUM(prompt_tokens) AS total_prompt_tokens,
        SUM(completion_tokens) AS total_completion_tokens,
        ROUND((SUM(prompt_tokens) * 0.00015 + SUM(completion_tokens) * 0.00060) / 1000, 4) AS estimated_usd
    FROM ask_log
    GROUP BY 1
    ORDER BY 1 DESC;

CREATE VIEW low_confidence_asks AS
    SELECT question, top_score, mode_used, asked_at
    FROM ask_log
    WHERE top_score < 0.01
    ORDER BY asked_at DESC;
```

---

## Query Patterns (SQL + Vector)

### Cache lookup (near-duplicate question detection)

```sql
-- Is there a cached answer for a similar question?
SELECT answer, sources_json, similarity
FROM (
    SELECT answer, sources_json,
           1 - (embedding <=> $1::vector) AS similarity
    FROM ask_cache
    WHERE expires_at IS NULL OR expires_at > NOW()
) ranked
WHERE similarity > 0.97    -- threshold: near-duplicate
ORDER BY similarity DESC
LIMIT 1;
```

### Retrieve top-k chunks with metadata filter

```sql
SELECT id, chunk_text, source_path, page_number,
       banner_module, banner_version,
       1 - (embedding <=> $1::vector) AS score
FROM chunks
WHERE source_type = $2
  AND ($3 = '' OR banner_module ILIKE $3)
  AND ($4 = '' OR banner_version = $4)
ORDER BY embedding <=> $1::vector
LIMIT $5;
```

### Analytics: which questions go to web search?

```sql
SELECT question, top_score, mode_used, asked_at
FROM ask_log
WHERE mode_used IN ('web', 'hybrid')
  AND asked_at > NOW() - INTERVAL '30 days'
ORDER BY top_score ASC
LIMIT 50;
```

The result identifies knowledge gaps — questions the local index can't answer, signaling which
documents are missing.

### Analytics: cost by module

```sql
SELECT
    a.source_type,
    COUNT(*) AS total_asks,
    SUM(a.prompt_tokens + a.completion_tokens) AS total_tokens,
    ROUND(SUM(a.prompt_tokens * 0.00015 + a.completion_tokens * 0.00060) / 1000, 2) AS cost_usd
FROM ask_log a
WHERE a.asked_at > NOW() - INTERVAL '30 days'
GROUP BY 1
ORDER BY cost_usd DESC;
```

---

## Hybrid Architecture: Both Backends

The most pragmatic path: use each tool for what it's best at.

```
                           ┌──────────────────┐
                           │   PostgreSQL      │
                           │                  │
POST /banner/ask ─────────►│  ask_log INSERT  │
                           │  cache lookup    │
                           │                  │
                           │  pgvector search │◄── Option B (replace Azure Search)
                           └──────────────────┘
                                    │
                                    ▼ (cache miss)
                           ┌──────────────────┐
                           │  Azure AI Search │◄── Option A (keep Azure Search)
                           │  hybrid search   │
                           └──────────────────┘
                                    │
                                    ▼
                           ┌──────────────────┐
                           │  Azure OpenAI    │
                           │  chat completion │
                           └──────────────────┘
                                    │
                                    ▼
                           ┌──────────────────┐
                           │   PostgreSQL      │
                           │  cache write     │
                           │  ask_log UPDATE  │
                           └──────────────────┘
```

---

## Other Database Options

### Qdrant (vector-native)

A purpose-built vector database. Better throughput than pgvector at scale; rich filtering.
Trade-off: another service to operate; less familiar SQL-like interface; no relational joins.
Best fit: if the index grows to millions of chunks and pgvector search latency becomes the bottleneck.

### SQLite + sqlite-vec

For single-instance, low-traffic deployments. Zero operational overhead — embedded in the binary.
`sqlite-vec` is a SQLite extension that adds vector search. Useful for local dev and small teams.
Not suitable for multi-instance deployments or heavy write loads.

### Weaviate / Milvus / Chroma

Other vector databases. All solve the same problem as pgvector but with more operational complexity.
Chroma is the simplest (Python-native) but has a Go client. Not recommended unless there's a
specific feature need that pgvector can't satisfy.

### Redis (for cache only)

If semantic caching is the only goal and PostgreSQL feels like overkill, Redis with the
`redis-stack` module (RedisSearch + RedisJSON) supports vector similarity search and has native TTL.
Azure Cache for Redis is available. Simpler than pgvector for a pure cache use case.

---

## Go Database Layer

### `pgx` — recommended PostgreSQL driver

```go
// go get github.com/jackc/pgx/v5
// go get github.com/jackc/pgx/v5/pgxpool

pool, err := pgxpool.New(ctx, cfg.DatabaseURL)

// Querying
rows, err := pool.Query(ctx,
    "SELECT id, chunk_text, 1 - (embedding <=> $1) AS score FROM chunks ORDER BY embedding <=> $1 LIMIT $2",
    pgvector.NewVector(embedding), topK,
)

// Inserting
_, err = pool.Exec(ctx,
    "INSERT INTO chunks (id, chunk_text, embedding, source_type, ...) VALUES ($1, $2, $3, $4, ...)",
    chunk.ID, chunk.Text, pgvector.NewVector(chunk.Embedding), chunk.SourceType,
)
```

pgvector Go support: `github.com/pgvector/pgvector-go`

```go
// go get github.com/pgvector/pgvector-go
import "github.com/pgvector/pgvector-go"

// Register the vector type with pgx
pgxpool.Config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
    return pgvector.RegisterTypes(ctx, conn)
}
```

### No ORM — stay consistent with the rest of the codebase

The existing design philosophy: no ORM, explicit SQL, structs marshalled directly. A SQL-first
approach fits naturally — write raw SQL, scan results into structs, handle errors explicitly.

```go
// internal/store/chunks.go — follows same pattern as internal/azure/search.go
type ChunkStore struct {
    pool *pgxpool.Pool
}

func (s *ChunkStore) InsertChunk(ctx context.Context, chunk ChunkDocument) error {
    _, err := s.pool.Exec(ctx,
        `INSERT INTO chunks (id, chunk_text, embedding, source_type, banner_module, banner_version, ...)
         VALUES ($1, $2, $3, $4, $5, $6, ...)
         ON CONFLICT (id) DO UPDATE SET chunk_text = EXCLUDED.chunk_text, embedding = EXCLUDED.embedding`,
        chunk.ID, chunk.Text, pgvector.NewVector(chunk.Embedding), chunk.SourceType, chunk.BannerModule, chunk.BannerVersion,
    )
    return err
}

func (s *ChunkStore) Search(ctx context.Context, embedding []float32, req SearchRequest) ([]SearchResult, error) {
    rows, err := s.pool.Query(ctx, hybridSearchSQL, pgvector.NewVector(embedding), req.SourceType, req.QueryText, req.TopK)
    // ...scan rows into []SearchResult
}
```

### Database URL from config

```go
// config/config.go
DatabaseURL string  // e.g. "postgres://user:pass@localhost:5432/omnivore?sslmode=disable"
```

Set from env var `DATABASE_URL`. When not set, skip all PostgreSQL initialization — keeps backward
compatibility for deployments that only use Azure Search.

---

## Trade-off Summary

| Dimension | Azure AI Search only | Azure Search + PostgreSQL | PostgreSQL + pgvector only |
|-----------|---------------------|--------------------------|--------------------------|
| Vendor lock-in | High (Azure) | Medium | Low (runs anywhere) |
| Operational complexity | Low (managed) | Medium (add PG) | Medium (manage PG) |
| Vector search quality | Excellent (HNSW, semantic reranking) | Excellent + portable | Very good (HNSW in pgvector) |
| Metadata filtering | Good (OData) | Excellent (SQL) | Excellent (SQL) |
| Analytics / reporting | None | Full SQL | Full SQL |
| Cost (Azure) | Search + storage cost | Search + PG cost | PG cost only |
| Portability | Azure-only | Mostly portable | Fully portable |
| Local dev without Azure | Not possible | Partial | Fully local (Docker) |
| Migration effort | — | Low (add PG alongside) | High (replace search client) |
| Backup / recovery | Azure-managed | Standard pg_dump | Standard pg_dump |

---

## Priority & Next Steps

**Phase 1 (no breaking changes):**
1. Add PostgreSQL for operational data only — `ask_log`, `feedback`, `ingest_jobs`
2. No changes to search path — Azure Search still handles all vector queries
3. Unlocks: cost analytics, feedback loop, durable job state

**Phase 2 (optional, bigger impact):**
3. Add semantic cache table (`ask_cache`) in PostgreSQL with pgvector
4. Wrap cache lookup before every RAG call — cuts cost on repeated questions

**Phase 3 (optional, eliminate Azure Search):**
5. Dual-write chunks to PostgreSQL during ingestion
6. Shadow-query pgvector alongside Azure Search to validate result parity
7. Gradually shift query traffic to pgvector
8. Decommission Azure Search resource

**Never needed:**
- An ORM (raw pgx + SQL stays consistent with the codebase philosophy)
- A separate migration tool for Phase 1 (hand-written `CREATE TABLE` in a `migrations/` folder is sufficient)
- Multiple database types (PostgreSQL handles relational, vector, cache, and full-text in one service)
