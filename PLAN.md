# Ask Banner — Implementation Plan

> **Branch:** `feat/chatbot`
> **TDD mandate:** Every task that touches Go code follows Red → Green → Refactor.
> Run `go test ./... -v` to verify at each step (CGO not available; omit `-race`).

---

## Completed (Archived)

| Phase | Description | Status |
|-------|-------------|--------|
| 0 | Replace 6 student intents with 5 internal-user intents | ✅ Done |
| 1 | Fix escalate threshold code vs spec mismatch (0.01 → 0.5) | ✅ Done |
| 2 | Simplify routing: collapse `student`/`general` → `banner`; 400 on invalid source | ✅ Done |
| 3 | Classifier comments update | ✅ Done |
| 4 | Agent 8 (Internal Chatbot) documented in CLAUDE_AGENTS.md | ✅ Done |
| 5 | Wiki docs updated | ✅ Done |

---

## Open Issue: Escalate Threshold Miscalibration

### Observed Evidence

```
Query: "What changed in Banner?"
confidence: 0.033   sources: [3 docs]   escalate: true   ← WRONG — useful answer
```

```
Query: "What changed in Banner 9.3.37?"
confidence: 0.000   sources: []         escalate: true   ← CORRECT — nothing indexed
```

### Root Cause

CLAUDE.md specifies `confidence < 0.5 → escalate`. This was written assuming a normalized
0–1 confidence scale. Azure AI Search returns raw hybrid scores (BM25 + semantic re-ranker)
that cluster between **0.01 and 0.05** for valid, well-grounded answers. A threshold of 0.5
causes every real answer to escalate, making the chatbot useless for any indexed content.

### Objectively Correct Resolution

**Two-signal escalation:**

| Signal | Meaning | Reliability |
|--------|---------|-------------|
| `retrieval_count == 0` | Index returned no documents | Hard binary — always reliable |
| `confidence < threshold` | Index returned docs but score is suspiciously low | Soft — requires calibration |

The **only reliable escalation signal today** is `retrieval_count == 0`. Any score > 0 with
sources present means Azure AI Search found semantically relevant chunks — the answer has
documentary backing.

A soft score floor is still valuable (e.g., protect against a 0.001 tangential hit), but it
must be calibrated from real query data, not assumed. Based on the one observed data point
(0.033 = good answer), any threshold ≥ 0.01 would have been fine; 0.5 is 15× too high.

**Interim decision:** `escalate = retrieval_count == 0 || confidence < 0.01`

This preserves a safety floor against near-zero-score tangential matches while not escalating
real answers. The calibration agent (Phase B) will validate or revise this floor with real data.

---

## Phase A — Calibrate Azure AI Search Score Distribution

**Goal:** Collect real score data across a range of queries to determine the correct floor.
**Agent required:** Agent 7 (Index Health & Diagnostics) — already designed in CLAUDE_AGENTS.md.
**New agent required:** Agent 9 (Confidence Calibration) — see Phase B for design.

### Tasks

- [x] **A.1 — Run baseline diagnostic with Agent 7**

  **Prompt for implementer:**
  Use Agent 7 (CLAUDE_AGENTS.md § Agent 7) to run a diagnostic session against the live
  backend (http://localhost:8000). Execute the following test queries using the `banner_ask`
  tool and record the `sources[0].score` for each:

  **Query set:**
  ```
  # Should find results (Banner docs indexed):
  "What changed in Banner General?"
  "What is new in Banner 9.3.37.2?"
  "What are the Banner General release notes?"
  "What are the breaking changes in Banner?"
  "What support changes were made in Banner 8?"

  # Should NOT find results (not indexed or too specific):
  "What changed in Banner Finance 9.3.37?" (if Finance not indexed)
  "What are the Banner HR module changes?"
  "Banner Student 9.4.1 release notes"
  "What is the Banner General 8.26 changelog?"
  ```

  Record results in a table:
  | Query | retrieval_count | sources[0].score | Has useful answer? |
  |-------|----------------|------------------|--------------------|

  Run Agent 7 from `agents/diagnostics.py` once built, or curl the backend directly:
  ```bash
  curl -s -X POST http://localhost:8000/banner/ask \
    -H "Content-Type: application/json" \
    -d '{"question":"What changed in Banner General?","module_filter":"General","top_k":5}' | jq '{count:.retrieval_count, score:.sources[0].score, answer:.answer[:80]}'
  ```

- [x] **A.2 — Identify score distribution boundaries**

  **Prompt for implementer:**
  From the data collected in A.1, identify:
  1. **Minimum score for a genuinely useful answer** (has documentary evidence, answer makes sense)
  2. **Maximum score for a genuinely useless answer** (retrieval_count > 0 but answer is wrong/tangential)
  3. The natural gap between these two groups, if one exists

  Record the findings in `wiki/RUNBOOK.md` under a new section:
  "## Azure AI Search Score Distribution"

  Format:
  ```
  ## Azure AI Search Score Distribution
  Observed: YYYY-MM-DD  Backend version: [git sha]

  | Category | Score Range | Example |
  |----------|------------|---------|
  | Good answer | 0.XXX–0.XXX | "What changed in Banner General?" → 0.033 |
  | Tangential / useless | 0.XXX–0.XXX | [query] → [score] |
  | No results | 0 | (retrieval_count == 0) |

  Recommended floor: 0.0XX
  ```

- [x] **A.3 — Decide final threshold**

  **Prompt for implementer:**
  Based on A.2 findings, choose the escalate floor. Decision criteria:
  - **If no natural gap exists** (good and bad answers have overlapping scores): use
    `retrieval_count == 0` as the sole signal. Score threshold = 0.0 (disabled).
  - **If a clear gap exists** (e.g., good ≥ 0.02, bad ≤ 0.005): set floor midway in the gap.
  - **Minimum defensible floor:** 0.005 (protects against near-zero noise hits).
  - **Never set above 0.05** until significantly more data is collected.

  Document the chosen value and rationale in `wiki/RUNBOOK.md` (same section as A.2).
  Update `CLAUDE.md` escalate rule to match.

---

## Phase B — Fix Escalate Logic (TDD)

**Goal:** Update `internal/adapter/client.go:mapResponse` to use the calibrated threshold.
All changes follow Red → Green → Refactor.

### Tasks

- [x] **B.1 — Write failing tests for `retrieval_count == 0` hard gate**

  **Prompt for implementer:**
  Open `internal/adapter/client_test.go`. Confirm `TestAdapterClient_BannerAsk_NoResults_Escalates`
  already exists and passes (it tests `retrieval_count == 0 → escalate`). If it does, mark this
  task done. If it doesn't, add it:
  ```go
  func TestAdapterClient_NoResults_AlwaysEscalates(t *testing.T) {
      // even if score were somehow non-zero, zero retrieval_count must escalate
      raw := ragAskResponse{RetrievalCount: 0, Sources: []ragSourceChunk{}}
      resp := mapResponse(raw)
      assert.True(t, resp.Escalate)
      assert.Zero(t, resp.Confidence)
  }
  ```
  Run `go test ./internal/adapter/... -v` — must be **GREEN** (gate already exists).

- [x] **B.2 — Write failing test for calibrated floor**

  **Prompt for implementer:**
  In `internal/adapter/client_test.go`, update `TestAdapterClient_BannerAsk_LowConfidence_SetsEscalate`
  (currently uses score 0.3) to use the threshold value decided in A.3. Also add:

  ```go
  func TestAdapterClient_ScoreAboveFloor_DoesNotEscalate(t *testing.T) {
      // score at or just above the floor with results present must NOT escalate
      srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
          json.NewEncoder(w).Encode(ragAskResponse{
              Answer:         "Banner General 9.3.37 changed X.",
              RetrievalCount: 3,
              Sources:        []ragSourceChunk{{Score: 0.033}}, // observed real-world score
          })
      }))
      defer srv.Close()
      client := NewAdapterClient(srv.URL)
      result, err := client.AskBanner(context.Background(), "What changed in Banner?", AskOptions{})
      require.NoError(t, err)
      assert.False(t, result.Escalate, "score 0.033 with 3 sources should not escalate")
  }
  ```

  Run `go test ./internal/adapter/... -v` — `TestAdapterClient_ScoreAboveFloor_DoesNotEscalate`
  must be **RED** while 0.5 threshold is in place. This is the failing test that drives the fix.

- [x] **B.3 — Fix threshold in adapter/client.go**

  **Prompt for implementer:**
  In `internal/adapter/client.go`, update `mapResponse`:
  ```go
  // Escalate if no documents retrieved (hard gate) or score is below the calibrated floor.
  // Azure AI Search hybrid scores for this index cluster 0.01–0.05 for valid results.
  // Threshold derived from empirical calibration — see wiki/RUNBOOK.md § Score Distribution.
  escalate := raw.RetrievalCount == 0 || confidence < [VALUE_FROM_A.3]
  ```
  Replace `[VALUE_FROM_A.3]` with the threshold decided in Phase A (interim: `0.01`).

  Run `go test ./... -v` — `TestAdapterClient_ScoreAboveFloor_DoesNotEscalate` must be
  **GREEN**. All other tests must remain **GREEN**. If any break, fix them.

- [x] **B.4 — Update CLAUDE.md escalate rule**

  **Prompt for implementer:**
  In `CLAUDE.md`, find the line:
  ```
  Confidence = sources[0].score; Escalate = true when confidence < 0.5 or retrieval_count == 0
  ```
  Replace with:
  ```
  Confidence = sources[0].score (raw Azure AI Search hybrid score; typical range 0.01–0.05 for valid results).
  Escalate = true when retrieval_count == 0 (hard gate) OR confidence < [threshold from RUNBOOK].
  NOTE: 0.5 is NOT a valid threshold for this index — Azure scores never reach that range.
  ```

---

## Phase C — Agent 9: Confidence Calibration Agent

**Goal:** Add a reusable calibration agent to `wiki/CLAUDE_AGENTS.md` so any team member can
re-run score distribution analysis as the index grows (new documents change score distributions).

### Tasks

- [x] **C.1 — Design and document Agent 9 in CLAUDE_AGENTS.md**

  **Prompt for implementer:**
  Append Agent 9 to `wiki/CLAUDE_AGENTS.md` using the design in the "Agent 9" section
  added to that file (see current state of CLAUDE_AGENTS.md § Agent 9).

  Agent 9 uses Agent 7's tools (`banner_ask`, `index_stats`) but drives a structured
  calibration protocol: runs a fixed set of known-good and known-bad test queries,
  records scores, and outputs a calibration report with a recommended threshold.

  The agent must:
  - Accept a module_filter parameter to calibrate per-module (Finance scores may differ from General)
  - Run at minimum 5 known-good and 5 boundary queries
  - Output a Markdown table: query, retrieval_count, score, has_useful_answer, verdict
  - Conclude with: recommended floor, confidence in recommendation, next review date

- [x] **C.2 — Add Agent 9 to Tool Reference table in CLAUDE_AGENTS.md**

  **Prompt for implementer:**
  In the Tool Reference table, Agent 9 reuses existing tools. Add a note row:
  ```
  | (calibration protocol) | POST | `/banner/ask` | 9 |
  | (calibration protocol) | GET  | `/index/stats` | 9 |
  ```

---

## Phase D — Documentation Sweep

### Tasks

- [x] **D.1 — Update CLAUDE.md**

  Already covered in B.4. Additionally: confirm the "Non-negotiable rules" section
  escalate rule is updated. Confirm the backend API comment block still accurately
  describes `sources[0].score`.

- [x] **D.2 — Add score distribution section to wiki/RUNBOOK.md**

  **Prompt for implementer:**
  Create or update `wiki/RUNBOOK.md`. Add section "## Azure AI Search Score Behavior"
  explaining:
  - Scores are raw hybrid (BM25 + semantic) values, NOT normalized confidence
  - Typical range for this index: 0.01–0.05 (update after Phase A data collection)
  - `retrieval_count == 0` is the reliable escalation trigger
  - The score floor is a secondary guard against near-zero noise, not a quality gate
  - Include the calibration query results table from Phase A.2

- [x] **D.3 — Update wiki/CHATBOT.md escalation section**

  **Prompt for implementer:**
  In `wiki/CHATBOT.md`, find any reference to the escalate threshold (0.5 or 0.01).
  Update the description of the `escalate` field in the `/chat/ask` response:
  ```
  escalate: true when:
    - retrieval_count == 0 (nothing indexed for this query)
    - confidence < [calibrated floor] (near-zero score even with some results)
  NOTE: confidence values for this index are typically 0.01–0.05. A value of 0.033
  with sources present is a GOOD answer, not a low-confidence one.
  ```

---

## Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `score=0.033, retrieval_count=3` → `escalate: false`
- [ ] `score=0, retrieval_count=0` → `escalate: true`
- [x] CLAUDE.md escalate rule no longer references 0.5
- [x] wiki/RUNBOOK.md has score distribution section with real data
- [x] Agent 9 documented in CLAUDE_AGENTS.md
- [ ] `/chat/ask` returns a useful answer for "What changed in Banner?"

---

## Not In Scope

- Changes to the go-omnivore-rag backend scoring model
- Modifying Azure AI Search index configuration
- Botpress flow changes (the `escalate` boolean drives those; logic change is transparent)
- gRPC changes

---

## Phase E — Banner User Guide Q&A

**Goal:** Let the chatbot answer "how to use Banner" questions by routing to the indexed
user guide PDFs (`source_type=banner_user_guide`). No version/year filters needed — these
are functional user guides, not release notes.

**Backend reality (do not modify the backend):**
- User guide PDFs sit in `data/docs/banner/<module>/use/` folders.
- The ingest pipeline already detects `/use/` paths → tags chunks as `banner_user_guide`.
- `POST /banner/:module/ask` (ModuleAsk handler) accepts `source_type` in the request body.
  Passing `source_type=banner_user_guide` scopes search to user guide chunks only.
- `/banner/student/ask` (StudentAsk handler) hardcodes `SourceTypeBannerGuide` — already works.

**Available PDFs (as of 2026-04-16):**
- `data/docs/banner/general/use/Banner General - Use - Ellucian.pdf` — General module
- `data/docs/banner/student/use/Banner Student - Use - Ellucian.pdf` — Student module
- `data/docs/banner/finance/use/` — Finance module (PDFs TBD)

**New adapter method:**
```go
AskBannerGuide(ctx context.Context, question string, module string) (AdapterResponse, error)
// Calls POST /banner/:module/ask with body {"question":"...","source_type":"banner_user_guide"}
// No version_filter, no year_filter — never set them for user guide queries.
```

**New intent: `BannerUsage`** (6th intent, extends the 5-intent set)

Covers questions about navigating Banner ERP — forms, fields, screens, lookups, workflows.
Differentiates from `SopQuery` (internal IT procedures) and `BannerAdmin` (config/setup).

Keyword scoring: each match = word_count × 0.3. Multi-word phrases beat SopQuery's generic
"how to" (0.6) when more specific phrases match (e.g., "how to use" = 0.9).

**New source values for `/chat/ask`:**
- `source=user_guide` → module=General → `AskBannerGuide(ctx, msg, "general")`
- `source=user_guide_student` → module=Student → `AskBannerGuide(ctx, msg, "student")`
- `source=user_guide_finance` → module=Finance → `AskBannerGuide(ctx, msg, "finance")`
  (returns 400 until finance user guide PDFs are ingested)

**Intent → source routing addition:**
`BannerUsage` → `user_guide` (default module=General)

---

### Tasks

- [ ] **E.1 — TDD: `BannerUsage` intent keywords**

  **Prompt for implementer:**
  In `internal/intent/classifier.go`, add `BannerUsage` as a 6th intent. Add to `IntentConfig`
  and `DefaultIntentConfig`. Keyword set (start with these, refine based on test failures):
  ```go
  BannerUsage: []string{
      "how to use", "navigate banner", "banner navigation",
      "banner form", "banner screen", "banner menu",
      "user guide", "where do i", "look up a",
      "what is the banner", "how do i find",
  },
  ```

  TDD sequence:
  1. **RED** — add tests in `internal/intent/classifier_test.go`:
     - `"How do I navigate the Banner main menu?"` → `BannerUsage`
     - `"Where do I find the journal entry form in Banner?"` → `BannerUsage`
     - `"How to use the Banner Finance module"` → `BannerUsage` (not SopQuery)
     - `"How to restart the Banner server"` → `SopQuery` (must NOT change)
     - `"What changed in Banner Finance?"` → `BannerRelease` (must NOT change)
  2. **GREEN** — add `BannerUsage Intent = "BannerUsage"` constant, extend `IntentConfig`,
     add keywords to `DefaultIntentConfig`.
  3. **REFACTOR** — check that all pre-existing intent tests still pass.

  Run: `go test ./internal/intent/... -v`

- [ ] **E.2 — TDD: `AskBannerGuide` adapter method**

  **Prompt for implementer:**
  In `internal/adapter/client.go`, add:
  ```go
  // bannerGuideAskRequest is the JSON body for /banner/:module/ask with user guide source.
  type bannerGuideAskRequest struct {
      Question   string `json:"question"`
      SourceType string `json:"source_type"`
      TopK       int    `json:"top_k,omitempty"`
  }

  // AskBannerGuide queries /banner/:module/ask scoped to banner_user_guide source type.
  // No version or year filter — user guide docs are not versioned.
  func (c *AdapterClient) AskBannerGuide(ctx context.Context, question string, module string) (AdapterResponse, error) {
      path := fmt.Sprintf("/banner/%s/ask", strings.ToLower(module))
      body := bannerGuideAskRequest{
          Question:   question,
          SourceType: "banner_user_guide",
      }
      // ... same POST + mapResponse pattern as AskBanner
  }
  ```

  TDD sequence:
  1. **RED** — in `internal/adapter/client_test.go`, add:
     - `TestAdapterClient_AskBannerGuide_Success` — httptest server returns valid response,
       assert `Escalate==false`, `Confidence > 0`, URL path == `/banner/general/ask`,
       body has `source_type=banner_user_guide`, no `version_filter`/`year_filter`
     - `TestAdapterClient_AskBannerGuide_NoResults_Escalates` — server returns
       `retrieval_count=0`, assert `Escalate==true`
     - `TestAdapterClient_AskBannerGuide_Module` — assert URL path uses the provided module
       (e.g., `/banner/student/ask`)
  2. **GREEN** — implement `AskBannerGuide`.
  3. **REFACTOR** — extract any shared HTTP POST + mapResponse helper if >2 methods share it.

  Also add `AskBannerGuide` to the `AdapterClient` interface in `api/handlers.go`.

  Run: `go test ./internal/adapter/... -v`

- [x] **E.3 — TDD: `user_guide` source routing in `/chat/ask`**

  **Prompt for implementer:**
  In `api/handlers.go`:
  1. Add `AskBannerGuide(ctx context.Context, question string, module string) (AdapterResponse, error)`
     to the `AdapterClient` interface.
  2. In `sourceFromIntent`, map `BannerUsage` → `"user_guide"`.
  3. In `askHandler`, add cases:
     ```go
     case "user_guide":
         resp, err = client.AskBannerGuide(r.Context(), req.Message, "general")
     case "user_guide_student":
         resp, err = client.AskBannerGuide(r.Context(), req.Message, "student")
     case "user_guide_finance":
         resp, err = client.AskBannerGuide(r.Context(), req.Message, "finance")
     ```
  4. Add `"user_guide"`, `"user_guide_student"`, `"user_guide_finance"` to the valid source
     reject-list guard (they must NOT return 400).

  TDD sequence:
  1. **RED** — in `api/handlers_test.go`, add:
     - `TestChatAsk_UserGuide_RoutesToGeneral` — POST `{source:"user_guide"}`, mock client
       receives `AskBannerGuide` call with module="general"
     - `TestChatAsk_UserGuide_Student` — POST `{source:"user_guide_student"}`, module="student"
     - `TestChatAsk_UserGuide_Finance` — POST `{source:"user_guide_finance"}`, module="finance"
     - `TestChatAsk_BannerUsageIntent_RoutesToUserGuide` — POST `{intent:"BannerUsage"}` with
       no source field → resolved to `user_guide` → module="general"
  2. **GREEN** — wire intent+source routing.
  3. **REFACTOR** — ensure mock interface in test file includes `AskBannerGuide`.

  Run: `go test ./api/... -v`

- [x] **E.4 — Update CLAUDE.md**

  **Prompt for implementer:**
  In `CLAUDE.md`:
  1. Add `BannerUsage` to the intent set (extend from 5 to 6 intents):
     ```
     BannerUsage    → questions about navigating Banner forms, screens, fields, lookups, workflows
     ```
  2. Add to the intent → backend routing table:
     ```
     BannerUsage    → /banner/general/ask  source_type=banner_user_guide  module=General
     ```
  3. Add new source override values:
     ```
     source=user_guide         → /banner/general/ask  source_type=banner_user_guide
     source=user_guide_student → /banner/student/ask  source_type=banner_user_guide
     source=user_guide_finance → /banner/finance/ask  source_type=banner_user_guide
     ```
  4. Add note: "No version_filter or year_filter for user_guide sources — user guide PDFs
     carry no version metadata."

- [x] **E.5 — Document Agent 10 in wiki/CLAUDE_AGENTS.md**

  **Prompt for implementer:**
  Append Agent 10 (Banner User Guide Navigation Agent) to `wiki/CLAUDE_AGENTS.md`.
  See the Agent 10 section already added below. Add to TOC and Tool Reference table.

- [x] **E.6 — Update wiki/CHATBOT.md**

  **Prompt for implementer:**
  In `wiki/CHATBOT.md`:
  1. Add `BannerUsage` to the intent set table.
  2. Add `user_guide`, `user_guide_student`, `user_guide_finance` to the source override table.
  3. Add a new section "## User Guide Q&A" describing when to use `user_guide` vs `banner`
     and the no-version-filter rule.

---

### Phase E Acceptance Criteria

- [ ] `go test ./... -v` passes with 0 failures
- [ ] `intent="BannerUsage"` + no source → routes to `AskBannerGuide(..., "general")`
- [ ] `source="user_guide_student"` → routes to `AskBannerGuide(..., "student")`
- [ ] `source="user_guide"` request body has `source_type=banner_user_guide`, no `version_filter`
- [ ] "How do I navigate the Banner main menu?" → intent `BannerUsage` (not SopQuery)
- [ ] "How to restart the Banner server" → intent `SopQuery` (unchanged)
- [ ] CLAUDE.md documents 6th intent and 3 new source values
- [ ] Agent 10 documented in CLAUDE_AGENTS.md
