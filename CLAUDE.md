# Ask Banner — GoLand AI Context

## What this is
Thin Botpress adapter over the go-omnivore-rag backend.
Student-facing Tier 0 chatbot for Banner ERP Q&A.

## Backend API (go-omnivore-rag — do not modify)
POST /banner/ask       { question (required), module_filter?, version_filter?, year_filter?, top_k? }
POST /sop/ask          { question (required), top_k? }
POST /banner/summarize/full  { filename (required), banner_module?, banner_version?, top_k? }
Returns rag.AskResponse:  { answer, question, retrieval_count, sources[] }
sources[0].score = confidence value (0–1, from Azure AI Search)

## This repo — new code only
internal/adapter   → HTTP client wrapping go-omnivore-rag
internal/intent    → keyword intent classifier (6 intents)
internal/sentiment → rule-based sentiment analyzer
api/handlers       → /chat/* endpoints consumed by Botpress

## Non-negotiable rules
- STRICT TDD: test first (red), then implement (green), then refactor
- No external dependencies beyond stdlib + testify
- Handlers return structured JSON errors — never leak upstream error messages
- Confidence = sources[0].score; Escalate = true when confidence < 0.5 or retrieval_count == 0
- All handlers accept injected interfaces — no concrete types in constructors

## Test runner
go test ./... -v -race

## Intent → backend routing table
RegistrationBanner → /banner/ask  module_filter=Student
FinanceBanner      → /banner/ask  module_filter=Finance
TranscriptSop      → /sop/ask
HoldsSop           → /sop/ask
ReleaseSummary     → /banner/summarize/full
General            → /banner/ask  (no filter)