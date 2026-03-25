---
title: "Building go-omnivore-rag, Part 2: From PDFs to Answers"
date: 2026-03-24
description: "The ingestion pipeline, chunking strategy, hybrid search, and the RAG query loop — how go-omnivore-rag turns raw PDFs into grounded, cited answers."
tags: ["go", "rag", "azure-ai-search", "openai", "vector-search", "pdf-parsing"]
series: ["go-omnivore-rag"]
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

## The SOP Pipeline

After the Banner ingestion pipeline was working, a second document type entered the picture: Standard Operating Procedures.

SOPs live in a different world from release notes. Where a Banner PDF arrives once per module per release and contains structured, predictable content, a SOP is a Word document with a procedural structure: numbered sections, sub-steps, assumptions, references. They answer a different kind of question — not *"what changed?"* but *"how do I actually do this?"*

Two things were immediately clear: SOPs needed their own ingestion path, and the existing character-based chunker was the wrong tool for them.

### Reading Word Documents Without a Library

`.docx` files are ZIP archives. Inside every `.docx` is a file called `word/document.xml`, which is OOXML — a verbose XML format that represents the document's paragraphs, runs, and styles.

Rather than pulling in an external Word parsing library, `docx.go` opens the ZIP directly and reads the XML with a token-based decoder from `encoding/xml`. No external dependencies. Each paragraph is parsed into a `DocxParagraph` that preserves the paragraph's style name, whether it belongs to a numbered list, and its indent level:

```go
type DocxParagraph struct {
    Style      string // "Heading1", "Heading2", "Normal", "ListParagraph"
    Text       string
    IsNumbered bool
    NumLevel   int // 0-based; 0 = top-level list item
}
```

Legacy `.doc` files — the pre-2007 binary format — return a clear error and are skipped. The fix is always the same: save as `.docx` and re-ingest.

### Section-Aware Chunking

The Banner chunker cuts on character boundaries. That is the right choice for dense PDFs where content flows continuously across sections.

SOPs have explicit structure. A section heading like *"6. Detailed Procedures"* is a logical boundary. Everything below it until the next heading belongs together. A character-based chunker might slice a numbered step across chunks, losing the step's context. A section-aware chunker never does.

`sop_chunker.go` walks the parsed paragraphs and opens a new chunk at every heading. When a chunk is flushed, the accumulated body text goes into a `SopChunk` alongside the document's full heading hierarchy:

```go
type SopChunk struct {
    SOPNumber     string
    DocumentTitle string
    SectionTitle  string
    Text          string // breadcrumb + body, ready for embedding
}
```

### Breadcrumbs

The most important detail is the breadcrumb prepended to every chunk's text before embedding:

```
[SOP 154 — Procedure - Start, Stop Axiom] > 6. Detailed Procedures > 6.2 Stopping Axiom

Open the Axiom web interface at https://axiom.internal and navigate to...
```

This matters because retrieval is chunk-level. When a chunk lands in the model's context, the model sees only that chunk — not the heading that introduced it three paragraphs earlier, not the document title it came from. The breadcrumb puts that context back.

Every SOP chunk is self-describing. You can hand any chunk to the model in isolation and it knows exactly which document and section it is from.

### Two Heading Styles

Real SOPs are not always written the same way. Testing against actual SOP documents revealed two distinct styles:

- **Styled documents** — headings are tagged with Word paragraph styles: `Heading1`, `Heading2`, `Heading3`. The chunker reads the style directly.
- **Plain documents** — headings are `Normal` paragraphs that start with a numbered prefix like `6.2 Stopping Axiom`. The chunker detects these with a regex and a length guard (≤80 characters, so it does not misclassify long body sentences that happen to start with a number).

Both styles produce the same output. The detection logic lives in a single function — `effectiveHeadingLevel` — which returns a depth for any paragraph regardless of which style it uses.

### Cover-Page Stripping

Word documents include paragraphs that carry document metadata rather than procedural content: company name, change history table headers, table of contents entries. Indexing these would pollute retrieval — a search for "6.2 Stopping Axiom" should not return a table-of-contents entry that references it.

A small denylist of style names (`Company`, `Project`, `TOC1`, `TOC2`, `TOCHeading`, etc.) causes these paragraphs to be skipped before any chunk is built.

### A Separate Index Lane

SOP chunks are indexed with `source_type: "sop"` alongside `sop_number` and `document_title`. This makes them filterable independently from Banner release notes. A query about an upgrade procedure can search across both. A query scoped to SOPs excludes release notes entirely.

The routing decision happens in `ingestFile`: if the file path contains `/docs/sop/`, it goes to `ingestSopFile`. Otherwise it takes the standard page-based path.

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

**[Continue to Part 3: The Summarizer, Tooling, and What Comes Next →](../part-3-shipping-and-future/)**
