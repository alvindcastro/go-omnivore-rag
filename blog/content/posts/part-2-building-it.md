---
title: "Building go-banner-rag, Part 2: From PDFs to Answers"
date: 2026-03-24
description: "The ingestion pipeline, chunking strategy, hybrid search, and the RAG query loop — how go-banner-rag turns raw PDFs into grounded, cited answers."
tags: ["go", "rag", "azure-ai-search", "openai", "vector-search", "pdf-parsing"]
series: ["go-banner-rag"]
showToc: true
TocOpen: false
draft: false
---

The architecture was clear. Time to write code.

## The Ingestion Pipeline

Everything starts with the `/ingest` endpoint. You point it at a directory (or trigger a blob sync), and it walks the file tree, processes every PDF and text file it finds, and loads the content into Azure AI Search.

### PDF Parsing

PDF parsing in Go is not glamorous. The [`ledongthuc/pdf`](https://github.com/ledongthuc/pdf) library extracts text page by page. The output is raw — stray whitespace, bullet character artifacts from PDF rendering, occasional zero-width characters that silently break downstream processing.

Before anything useful could happen, the text needed cleaning:

```go
func sanitizeText(text string) string {
    replacements := map[string]string{
        "\u2022": " ", // bullet •
        "\u00b7": " ", // middle dot ·
        "\f":     " ", // form feed
        "\u200b": "",  // zero-width space
    }
    for old, new := range replacements {
        text = strings.ReplaceAll(text, old, new)
    }
    space := regexp.MustCompile(`\s+`)
    return strings.TrimSpace(space.ReplaceAllString(text, " "))
}
```

### Chunking Strategy

A single PDF page is usually too large to embed meaningfully and too large to fit cleanly in a prompt alongside several other pages. The system breaks text into configurable-size character windows with overlap.

But it does not cut blindly at the character limit. It walks backward from the boundary looking for a natural break — paragraph first, then sentence, then period, then comma, then space. This keeps chunks semantically coherent instead of slicing mid-sentence.

Chunks shorter than 50 characters are discarded. Each surviving chunk gets an **MD5 hash of its content** as its ID, which makes re-ingestion idempotent — the same content always produces the same ID, so you can re-ingest without creating duplicates.

### Metadata Extraction

This was one of the more satisfying pieces to build.

The folder structure `data/docs/general/2026/` contains all the signal needed. A regex against the filename pulls the version number. The parent directory name maps to a Banner module. The year folder contributes the year. This runs at ingest time with no user input required:

```
data/docs/
  general/
    2026/
      Banner_General_Release_Notes_9.3.37.2_8.26.2_February_2026.pdf
          → module: "general"
          → version: "9.3.37.2"
          → year: "2026"
```

### The Index Schema

The Azure AI Search index schema includes:

- `content` — the raw chunk text
- `contentVector` — 1536-dimension float array (Ada-002 output)
- `module`, `version`, `year` — filterable string fields
- `source`, `page` — for source citations in answers

The vector field is configured with HNSW parameters tuned for the dataset size:
- `M: 4` — connections per node
- `efConstruction: 400` — build-time quality
- `efSearch: 500` — query-time recall

Chunks flow to the index in batches of 100 via the Search REST API.

## The RAG Query Loop

With documents indexed, the query pipeline is straightforward.

### Hybrid Search

The `/ask` endpoint receives a question with optional filters:

```json
{
  "question": "What database prerequisites are required for Banner General 9.3.37.2?",
  "module": "general",
  "version": "9.3.37.2",
  "year": "2026"
}
```

The question is embedded with Ada-002, producing a 1536-dimension vector. That vector goes to Azure AI Search alongside the keyword query — the same question text. Azure AI Search merges the two result sets using **Reciprocal Rank Fusion (RRF)**, combining BM25 keyword ranking with cosine vector similarity.

OData filter expressions scope the results:

```
module eq 'general' and version eq '9.3.37.2' and year eq '2026'
```

Without filters, the search spans the full corpus. With filters, it returns only chunks from the relevant release notes — turning a broad document search into a focused version-specific assistant.

### Prompt Engineering

The system prompt establishes the model's persona and its constraints:

```
You are an expert Ellucian Banner ERP system assistant helping IT staff
and functional analysts understand upgrade requirements and release notes.

Answer ONLY using the provided context. If the answer is not in the
context, say so clearly. Do not guess or extrapolate.

Always cite the source chunk(s) you used to answer.
```

Temperature is set to `0.1`. This is not a creative writing task. The model's job is to extract and present information that is already in the context. Low temperature keeps answers factual and consistent.

### Retry Logic

The retry logic on the OpenAI client handles rate limiting that appears during batch operations:

```go
for attempt := 0; attempt < 3; attempt++ {
    resp, err := callOpenAI(payload)
    if err == nil {
        return resp, nil
    }
    if isRateLimited(err) {
        time.Sleep(15 * time.Second)
    }
}
```

Three attempts. Fifteen-second wait on HTTP 429s. Simple and sufficient.

## The Result

A functional analyst who previously opened a PDF and started scanning for the compatibility section can now send this:

```http
POST /ask
{
  "question": "What Java version is required for Banner General 9.3.37.2?",
  "module": "general",
  "version": "9.3.37.2"
}
```

And receive a grounded, cited answer in under three seconds.

The ingestion pipeline handles the tedium. The hybrid search handles the retrieval. The prompt keeps the model honest. Each piece does one thing well.

**[Continue to Part 3: The Summarizer, Tooling, and What Comes Next →](/posts/part-3-shipping-and-future/)**
