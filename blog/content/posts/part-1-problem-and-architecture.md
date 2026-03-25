---
title: "Building go-omnivore-rag, Part 1: The Problem Nobody Talks About"
date: 2026-03-24
description: "Every Banner upgrade ships a PDF. Someone has to read it. What if they didn't have to? This is the story of how go-omnivore-rag started — the pain point, the idea, and the architecture that emerged from a whiteboard session."
tags: ["go", "rag", "ai", "azure", "architecture", "ellucian-banner"]
series: ["go-omnivore-rag"]
cover:
  image: ""
  alt: "Architecture diagram of the go-omnivore-rag RAG pipeline"
  relative: false
showToc: true
TocOpen: false
draft: false
---

Every semester, something lands in the inbox of IT teams at universities across North America.

It's a PDF.

Sometimes it's 40 pages. Sometimes it's 80. It has a name like `Banner_General_Release_Notes_9.3.37.2_8.26.2_February_2026.pdf`, and the moment it arrives, someone has to figure out what changed, what broke, what needs to happen before the upgrade window, and whether the current Java version is still supported.

That someone is usually a functional analyst or a systems administrator buried under a dozen other priorities.

## The Domain

**Ellucian Banner** is the ERP backbone for hundreds of higher-education institutions — student records, financial aid, HR, finance, all of it running through a system that has existed in some form since the 1980s. It is critical infrastructure. And like all critical infrastructure, it gets updated regularly.

Each update ships with release notes that are technically thorough and practically exhausting to read.

The problem is not that the documentation is bad. It is actually quite good. The problem is that finding the one sentence that answers your specific question — *"Do we need to run a database migration before this update?"* — requires reading everything around it first.

I wanted to fix that.

## The Idea

The idea started simply: **what if you could just ask the release notes a question?**

The technology for this already existed. RAG — Retrieval-Augmented Generation — is the pattern where you:

1. Take a large document corpus
2. Break it into chunks
3. Convert those chunks into vector embeddings
4. Store them in a searchable index
5. At query time, find the most relevant chunks
6. Feed them as context to a language model
7. The model answers based only on what you retrieved — grounded, with citations

The question was not *whether* to use RAG. The question was *how to build it cleanly*.

## Three Hard Requirements

The first whiteboard session produced three non-negotiables:

**1. PDFs as-is.** No preprocessing pipeline, no manual reformatting. Drop a file in a folder, ingest it, done.

**2. Automatic metadata.** Banner release notes have a predictable naming convention (`Banner_<Module>_Release_Notes_<version>_<date>.pdf`) and live inside a predictable folder structure (`/docs/general/2026/`). The system should extract module, version, and year without anyone filling out a form.

**3. Hybrid search.** Pure vector search is good at semantic similarity but struggles with exact version numbers. Pure keyword search misses paraphrase. You need both.

## The Stack

The technology choices fell into place quickly:

| Layer | Choice | Reason |
|-------|--------|--------|
| Language | **Go 1.22** | Fast, minimal, excellent standard library |
| Web Framework | **Gin** | Lightweight, idiomatic |
| Embeddings | **Azure OpenAI — text-embedding-ada-002** | Hosted, affordable, 1536 dimensions |
| Chat | **Azure OpenAI — GPT-4o-mini** | Factual, low-cost completions |
| Vector Index | **Azure AI Search** | Native hybrid search (BM25 + HNSW vector) |
| Document Storage | **Azure Blob Storage** | Drop PDFs, sync on demand |

One deliberate choice: **no official SDKs for OpenAI or Search**. The Azure OpenAI and Azure AI Search clients are written as direct REST calls. When you build against a raw API, you understand exactly what is being sent and received. There are no abstraction layers to debug. For a project where cost visibility matters, this turned out to be the right call.

## The Architecture

The system follows a clean, linear pipeline:

```
Banner PDFs (Blob Storage or local folder)
              ↓
        /ingest endpoint
              ↓
  Extract → Chunk → Embed → Index
              ↓
        /ask endpoint
              ↓
  Embed question → Hybrid Search → Retrieve top chunks
              ↓
  Build grounded prompt → GPT-4o-mini → Answer + sources
```

Each stage has one job. No stage knows about the stages around it except through well-defined data shapes.

The project structure reflects this separation:

```
internal/
  api/        → Gin HTTP handlers and router
  azure/      → REST clients for OpenAI, Search, Blob
  ingest/     → PDF parsing, chunking, embedding, indexing
  rag/        → Query answering and summarization
config/       → Environment-based configuration
```

Roughly 2,000 lines of Go. Four direct dependencies.

## What's Next

With the architecture clear and the stack chosen, the next step was actually building it — and PDF parsing in Go is not glamorous.

**[Continue to Part 2: Building the Foundation →](../part-2-building-it/)**
