# Ask Banner — GoLand AI Context

## What this is
Thin Botpress adapter over the go-omnivore-rag backend.
Internal-user chatbot for Banner ERP Q&A: release notes, module changes, and SOPs.
Target audience: IT staff, functional analysts, Banner admins — not students.

## Backend API (go-omnivore-rag — do not modify)
POST /banner/ask       { question (required), module_filter?, version_filter?, year_filter?, top_k? }
POST /sop/ask          { question (required), top_k? }
POST /banner/summarize/full  { filename (required), banner_module?, banner_version?, top_k? }
Returns rag.AskResponse:  { answer, question, retrieval_count, sources[] }
sources[0].score = raw Azure AI Search hybrid score (NOT a normalized 0–1 confidence).
                   Observed range for valid answers: 0.01–0.05. See wiki/RUNBOOK.md § Score Distribution.

## This repo — new code only
internal/adapter   → HTTP client wrapping go-omnivore-rag
internal/intent    → keyword intent classifier (6 intents)
internal/sentiment → rule-based sentiment analyzer
api/handlers       → /chat/* endpoints consumed by Botpress

## Non-negotiable rules
- STRICT TDD: test first (red), then implement (green), then refactor
- No external dependencies beyond stdlib + testify
- Handlers return structured JSON errors — never leak upstream error messages
- Confidence = sources[0].score (raw Azure AI Search score; typical range 0.01–0.05 for valid results)
- Escalate = true when retrieval_count == 0 (hard gate) OR confidence < calibrated floor (see RUNBOOK)
- WARNING: 0.5 is NOT a valid escalate threshold for this index — Azure scores never reach that range
- All handlers accept injected interfaces — no concrete types in constructors

## Test runner
go test ./... -v
(CGO not available in this environment; omit -race)

## Intent set (6 intents — internal user audience)
BannerRelease  → questions about release notes, versions, upgrades, breaking changes
BannerFinance  → Finance module questions (GL, AR, budget, grants)
SopQuery       → IT operational procedures ("how to restart", "smoke test", "SOP", "process")
BannerAdmin    → general Banner config, admin, setup, module questions
BannerUsage    → how to USE Banner ERP — forms, screens, navigation, lookups, field meanings
General        → catch-all fallback

## Intent → backend routing table
Routing uses a two-level system: explicit `source` field wins; otherwise derived from `intent`.
/banner/ask with no module_filter returns 0 results — every query must carry a module context.

BannerRelease  → /banner/ask  module_filter=General
BannerFinance  → /banner/ask  module_filter=Finance
SopQuery       → /sop/ask
BannerAdmin    → /banner/ask  module_filter=General
BannerUsage    → /banner/general/ask  source_type=banner_user_guide  (user guide, no version/year filter)
General        → /banner/ask  module_filter=General

Source override (optional field in /chat/ask body):
  source=banner              → module_filter=General  (release notes / all non-Finance Banner)
  source=finance             → module_filter=Finance  (release notes)
  source=sop                 → /sop/ask
  source=user_guide          → /banner/general/ask  source_type=banner_user_guide
  source=user_guide_student  → /banner/student/ask  source_type=banner_user_guide
  source=user_guide_finance  → /banner/finance/ask  source_type=banner_user_guide
  source=auto                → derive from intent (same as omitting source)
  source=student             → INVALID (returns 400)
  source=general             → INVALID (returns 400)

NOTE: Never set version_filter or year_filter for user_guide sources — user guide PDFs
carry no version metadata and filtering by version will return 0 results.