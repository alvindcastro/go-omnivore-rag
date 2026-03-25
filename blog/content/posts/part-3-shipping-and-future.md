---
title: "Building go-omnivore-rag, Part 3: Shipping, Summarizers, and What Comes Next"
date: 2026-03-24
description: "Adding structured summarization for Banner release notes, debug tooling, Bruno API collections, and the roadmap ahead — cost monitoring, streaming, multi-document diffing, and a proper frontend."
tags: ["go", "rag", "summarization", "azure-openai", "developer-experience", "roadmap"]
series: ["go-omnivore-rag"]
showToc: true
TocOpen: false
draft: false
---

The `/ask` endpoint worked. Specific questions got specific, grounded answers. But during testing, a different pattern kept emerging.

*"I don't have a specific question yet. I just got this release note. What do I need to know?"*

## The Real Workflow

This is the first thing a functional analyst does when a Banner PDF lands. They skim it looking for four things:

1. **What changed?** New features, enhancements, behavior differences.
2. **What broke?** Removed functionality, deprecated APIs, end-of-support notices.
3. **What do I have to do?** Required steps before or after the upgrade.
4. **What are the prerequisites?** Java version, database version, dependent module versions.

The RAG system already had the information. It just didn't have an endpoint that answered these four questions automatically. That became the summarizer.

## The Summarization System

The summarizer exposes five endpoints:

| Endpoint | What it produces |
|----------|-----------------|
| `/summarize/changes` | New features and enhancements |
| `/summarize/breaking` | Removals, deprecations, end-of-support |
| `/summarize/actions` | Checklist of required steps for IT staff |
| `/summarize/compatibility` | Prerequisites, version requirements |
| `/summarize/full` | All four topics in one response |

### Topic-Specific Prompts

Instead of one general summarization prompt, each topic gets a specialized system prompt.

The `breaking` prompt explicitly instructs the model to focus on removals and deprecations:

```
You are a Banner ERP specialist analyzing release notes for breaking changes.
Identify and list:
- Removed features or functionality
- Deprecated APIs or configuration options
- End-of-life or end-of-support notices
- Behavior changes that may require code or configuration updates

Be specific and complete. Missing a breaking change has real consequences.
```

The `actions` prompt produces a checklist:

```
You are a Banner ERP specialist creating an upgrade checklist for IT staff.
Extract every required action from the release notes:
- Pre-upgrade steps
- Database migrations or schema changes
- Configuration updates
- Post-upgrade verification steps

Format as a numbered checklist. Be exhaustive.
```

Specialization produces better output than generalization. A model given a focused task performs better on that task.

### One Retrieval Pass, Four Generations

The full summarizer (`/summarize/full`) makes a single broad retrieval pass to gather context, then runs four generation calls against the same context in parallel. This avoids four redundant searches while still allowing each topic's generation to be focused.

The output is structured JSON:

```json
{
  "version": "9.3.37.2",
  "module": "general",
  "changes": "...",
  "breaking_changes": "...",
  "required_actions": "...",
  "compatibility": "..."
}
```

A functional analyst who previously spent twenty minutes skimming a PDF gets this in under ten seconds.

## Debug Tooling

Two additions made the system substantially easier to work with during development and after deployment.

### The `/debug/chunks` Endpoint

During early testing, it was unclear whether the ingestion pipeline was producing quality chunks. Were they too short? Were encoding artifacts surviving the sanitizer? This endpoint lets you query raw indexed chunks by module, version, and year. You see exactly what the retrieval system sees, which makes diagnosing retrieval failures much faster.

Debugging a RAG system without visibility into the index is guesswork. This turned guesswork into inspection.

### Bruno API Collection

The project includes a Bruno API collection covering all 16 endpoints, organized into folders:

```
apis/
  System/      → health, index stats, index create
  RAG/         → ask
  Ingestion/   → ingest
  Blob/        → list, sync
  Summarizer/  → changes, breaking, actions, compatibility, full
  Debug/       → chunks
```

Bruno stores collections as plain files in the project repository — not in a proprietary cloud database. Any developer who clones the repo gets a working API client immediately, without creating accounts or importing shared workspaces.

Small things that reduce friction compound over time.

## What Comes Next

The system works well for its primary purpose. Several directions are worth building next.

### Multi-Document Diffing

Right now you can ask about a specific version or search across all versions. What you cannot do easily is ask *"What changed between 9.3.36.0 and 9.3.37.2?"*

A diffing mode would retrieve chunks from two specific versions and ask the model to compare them. This is probably the most immediately useful missing feature for teams managing Banner upgrades across multiple years.

### Streaming Responses

The current `/ask` endpoint returns a complete response after the full generation cycle. For longer answers, users wait with no feedback.

Azure OpenAI supports server-sent events for streaming completions. Adding streaming to the Gin handler would make the interface feel significantly more responsive — first tokens appear immediately, and the full answer streams in.

### A Frontend

Right now the system is an API. Every interaction goes through a REST client. A simple web interface — a text box for the question, dropdowns for module and version, a response pane with source citations — would open the system to functional analysts who are not comfortable with API tools.

Go can serve HTML. The frontend does not need to be complex. It needs to be accessible.

### Structure-Aware Chunking

The current chunker is character-based with sentence-boundary awareness. Banner release notes have internal structure — numbered sections, tables, prerequisite lists — that the chunker ignores.

A structure-aware chunker that recognizes section headers and keeps version matrices intact would improve retrieval quality for compatibility questions, where the relevant information is often in a dense table of version requirements.

### Evaluation Harness

It is currently difficult to measure whether a change to chunk size, overlap, top-K, or prompt text makes answers better or worse. There is no systematic feedback loop.

Building a small evaluation suite — a set of known questions with expected answers, scored against retrieved context and model output — would enable confident iteration instead of guesswork. RAG systems without evals tend to drift.

### Cost Monitoring

Azure provides budget alerts, but the system itself does not track token consumption per request. Instrumenting the OpenAI client to log prompt tokens, completion tokens, and estimated cost per call would make it much easier to understand which operations are expensive and where to optimize.

The summarizer's `/full` endpoint makes multiple generation calls. Without per-request instrumentation, the cost profile is invisible.

## Closing Thoughts

The problem this project addresses — people spending time reading documents to find specific answers — is not unique to Banner or to higher education. Any domain with dense technical documentation has the same problem. Release notes, compliance documents, legal contracts, API changelogs, maintenance manuals.

The architecture is not novel. RAG has been a known pattern for several years. What makes this project useful is how directly a small amount of well-organized code maps to a real reduction in time spent on a tedious task.

The system is roughly 2,000 lines of Go. Four direct dependencies. Runs anywhere Docker runs. Costs almost nothing on Azure's free tier for low-volume usage.

The hardest part was not the code. It was figuring out exactly what questions people actually needed answered.

That is usually how it goes.

---

*The full source is on GitHub: [alvindcastro/go-omnivore-rag](https://github.com/alvindcastro/go-omnivore-rag)*
