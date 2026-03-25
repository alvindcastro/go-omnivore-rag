---
title: "We stopped reading Banner release notes. Here's what we built instead."
date: 2026-03-23
description: "Stop ctrl+F-ing through 80-page PDFs — ask your Banner release notes a question and get a cited answer in under three seconds."
tags: ["go", "rag", "azure", "openai", "ellucian-banner", "vector-search"]
series: ["go-banner-rag"]
showToc: false
draft: false
weight: 1
---

It's a tool that reads Banner release notes so your team doesn't have to. Drop a PDF in, ask it a question — *"what do I need to do before this upgrade?"* — and it pulls the answer straight from the document with a source citation. No hallucinations, no guessing. Just the relevant paragraph, found in seconds.

## What Banner admins actually care about

These are the real risks that make every upgrade stressful — and exactly what this tool helps surface before they become problems:

- Missing a required pre-upgrade step can break dependent modules
- Java and database version mismatches cause upgrade failures with no clear error
- Deprecated APIs removed in one release can silently break customizations
- Compatibility matrices span multiple modules — one version mismatch blocks the whole upgrade path
- Release notes for Finance, Student, and HR arrive separately and must be reconciled before a shared upgrade window

---

> **Built in Go. Powered by Azure OpenAI + Azure AI Search.**
> No frameworks. No hallucinations. ~2k lines of Go.
> [github.com/alvindcastro/go-banner-rag](https://github.com/alvindcastro/go-banner-rag)

---

**Read the full build story:**

- [Part 1 — The Problem Nobody Talks About](../part-1-problem-and-architecture/)
- [Part 2 — From PDFs to Answers](../part-2-building-it/)
- [Part 3 — Shipping, Summarizers, and What Comes Next](../part-3-shipping-and-future/)
