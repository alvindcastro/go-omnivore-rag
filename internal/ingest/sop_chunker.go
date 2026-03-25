// internal/ingest/sop_chunker.go
// Phase 3 — Section-aware SOP chunker.
//
// Handles two document styles observed in real SOPs:
//   - Styled:  Heading1/2/3/4 paragraphs (SOP122)
//   - Plain:   Normal paragraphs with numbered prefixes like "6.2 Stopping Axiom" (SOP154)
//
// Each chunk carries a breadcrumb prefix so the model always knows which SOP
// and section a chunk came from, even when retrieved in isolation:
//
//	[SOP 154 — Procedure - Start, Stop Axiom] > 6. Detailed Procedures > 6.2 Stopping Axiom
//	<body text>
package ingest

import (
	"regexp"
	"strings"
)

// SopChunk is one retrieval-ready unit from a SOP document.
type SopChunk struct {
	SOPNumber     string
	DocumentTitle string
	SectionTitle  string // most specific heading that introduces this chunk
	Text          string // breadcrumb + body, ready for embedding
}

// skipStyles are document-furniture paragraphs that carry no procedural content.
// Covers cover-page styles, change-history tables, and table-of-contents entries.
var skipStyles = map[string]bool{
	"Company": true, "Project": true, "Deliverable": true,
	"InformationPage": true, "TInformationPage": true,
	"TOCHeading": true, "TOC1": true, "TOC2": true, "TOC3": true,
}

// numberedSectionRe detects numbered section headings in Normal paragraphs.
// Matches patterns like:
//
//	"1. Purpose"
//	"3. Assumptions & Notes"
//	"6.1 Access the Server (DEV or PROD)"
//	"6.2 Stopping Axiom"
//
// Combined with a length guard (≤80 chars) to avoid matching long body
// sentences that happen to start with a number.
// Only applied to Normal-style paragraphs; ListParagraph items are excluded.
var numberedSectionRe = regexp.MustCompile(`^(\d+)(\.(\d+))?(\.(\d+))?\s*[.\-–]?\s+\S`)

// effectiveHeadingLevel returns the heading depth for a paragraph:
//   - 1–4 for actual Heading1–Heading4 styles
//   - 1 for numbered top-level Normal paragraphs  ("1. Purpose")
//   - 2 for numbered second-level Normal paragraphs ("6.1 Access")
//   - 0 for all other content (body text, list items, etc.)
func effectiveHeadingLevel(p DocxParagraph) int {
	if p.IsHeading() {
		return p.HeadingLevel()
	}
	// Numbered-section fallback: only for short Normal paragraphs.
	if p.Style != "Normal" || len(p.Text) > 80 {
		return 0
	}
	m := numberedSectionRe.FindStringSubmatch(p.Text)
	if m == nil {
		return 0
	}
	if m[3] != "" { // has a second numeric component, e.g. "6.1"
		return 2
	}
	return 1
}

// chunkSop splits the paragraphs of a SOP document into retrieval-ready chunks.
//
// A new chunk is started at every heading (any level). The current heading
// hierarchy is prepended as a breadcrumb to every chunk so that context is
// preserved regardless of which chunk is retrieved.
func chunkSop(paras []DocxParagraph, meta sopMetadata) []SopChunk {
	var (
		chunks           []SopChunk
		headings         [4]string // index 0 = H1, 1 = H2, 2 = H3, 3 = H4
		body             []string
		sectionTitle     string
		firstHeadingSeen bool // discard cover-page body before any real heading
	)

	flush := func() {
		if len(body) == 0 {
			return
		}
		text := buildChunkText(meta, headings[:], body)
		chunks = append(chunks, SopChunk{
			SOPNumber:     meta.number,
			DocumentTitle: meta.title,
			SectionTitle:  sectionTitle,
			Text:          text,
		})
		body = body[:0]
	}

	for _, p := range paras {
		if skipStyles[p.Style] {
			continue
		}

		level := effectiveHeadingLevel(p)
		if level > 0 {
			flush()
			firstHeadingSeen = true

			idx := level - 1
			if idx >= len(headings) {
				idx = len(headings) - 1
			}
			headings[idx] = p.Text
			// Clear any deeper heading levels so stale context doesn't bleed in.
			for i := idx + 1; i < len(headings); i++ {
				headings[i] = ""
			}
			sectionTitle = p.Text
		} else if firstHeadingSeen {
			body = append(body, p.Text)
		}
	}
	flush() // emit the final pending chunk

	return chunks
}

// buildChunkText assembles the breadcrumb + body text for a single chunk.
func buildChunkText(meta sopMetadata, headings []string, body []string) string {
	var sb strings.Builder

	// Breadcrumb: [SOP N — Title] > H1 > H2 > H3
	sb.WriteString("[SOP ")
	sb.WriteString(meta.number)
	sb.WriteString(" — ")
	sb.WriteString(meta.title)
	sb.WriteString("]")
	for _, h := range headings {
		if h != "" {
			sb.WriteString(" > ")
			sb.WriteString(h)
		}
	}

	sb.WriteString("\n\n")
	sb.WriteString(strings.Join(body, "\n"))

	return strings.TrimSpace(sb.String())
}
