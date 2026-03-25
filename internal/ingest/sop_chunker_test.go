package ingest

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ─── effectiveHeadingLevel ────────────────────────────────────────────────────

func TestEffectiveHeadingLevel_StyledHeadings(t *testing.T) {
	cases := []struct {
		style string
		text  string
		want  int
	}{
		{"Heading1", "SMOKE TEST", 1},
		{"Heading2", "Check the links", 2},
		{"Heading3", "Log in as student", 3},
		{"Heading4", "Sub step", 4},
		{"Normal", "Regular body text.", 0},
		{"ListParagraph", "Step one", 0},
		{"TOC1", "Table of contents entry", 0},
		{"Company", "Douglas College", 0},
	}
	for _, c := range cases {
		p := DocxParagraph{Style: c.style, Text: c.text}
		if got := effectiveHeadingLevel(p); got != c.want {
			t.Errorf("effectiveHeadingLevel(%q, %q) = %d, want %d", c.style, c.text, got, c.want)
		}
	}
}

func TestEffectiveHeadingLevel_NumberedNormal(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		// Level 1 — top-level numbered sections (SOP154 style)
		{"1. Purpose", 1},
		{"2. Scope", 1},
		{"6. Detailed Procedures", 1},
		{"10. Appendix", 1},
		// Level 2 — subsections
		{"6.1 Access the Server (DEV or PROD)", 2},
		{"6.2 Stopping Axiom", 2},
		{"6.3 Starting Axiom", 2},
		// Not matched — too long (> 80 chars)
		{"1. This is a very long sentence that exceeds the eighty character threshold and should not be treated as a heading.", 0},
		// Not matched — ListParagraph style (even if starts with number)
		// (handled by style check in effectiveHeadingLevel — tested separately below)
		// Not matched — no digit at start
		{"TEST SUMMARY", 0},
		{"Some body paragraph.", 0},
	}
	for _, c := range cases {
		p := DocxParagraph{Style: "Normal", Text: c.text}
		if got := effectiveHeadingLevel(p); got != c.want {
			t.Errorf("effectiveHeadingLevel(Normal, %q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestEffectiveHeadingLevel_ListParagraphNotHeading(t *testing.T) {
	// ListParagraph items that start with a number should never be headings
	// even if they look like "1. Do something"
	p := DocxParagraph{Style: "ListParagraph", Text: "1. Login to the server"}
	if got := effectiveHeadingLevel(p); got != 0 {
		t.Errorf("ListParagraph starting with number should return 0, got %d", got)
	}
}

func TestChunkSop_EmptyBodySkipped(t *testing.T) {
	// A heading immediately followed by another heading should not emit an empty chunk.
	meta := sopMetadata{number: "1", title: "T"}
	paras := []DocxParagraph{
		{Style: "Heading1", Text: "H1"},
		{Style: "Heading2", Text: "H2"}, // no body between H1 and H2
		{Style: "Normal", Text: "actual content"},
	}

	chunks := chunkSop(paras, meta)

	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk (empty H1 body skipped), got %d: %v", len(chunks), chunkTitles(chunks))
	}
	assertContains(t, chunks[0].Text, "actual content")
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("expected to contain %q\ngot: %s", sub, s)
	}
}

func assertNotContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("expected NOT to contain %q\ngot: %s", sub, s)
	}
}

func chunkTitles(chunks []SopChunk) []string {
	titles := make([]string, len(chunks))
	for i, c := range chunks {
		titles[i] = c.SectionTitle
	}
	return titles
}

// ─── Real-file integration test ───────────────────────────────────────────────

func TestChunkSop_RealFiles(t *testing.T) {
	files := []string{
		"../../data/docs/sop/SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx",
		"../../data/docs/sop/SOP154 - Procedure - Start, Stop Axiom.docx",
	}

	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			paras, err := extractDocxParagraphs(f)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}

			meta := parseSopFilename(f)
			if meta.number == "" {
				t.Fatalf("could not parse SOP number from %s", filepath.Base(f))
			}

			chunks := chunkSop(paras, meta)
			if len(chunks) == 0 {
				t.Fatal("no chunks produced")
			}

			fmt.Printf("\n===== %s — %d chunks =====\n", filepath.Base(f), len(chunks))
			for i, c := range chunks {
				lines := strings.Split(c.Text, "\n")
				preview := lines[0]
				if len(lines) > 2 {
					body := lines[2]
					if len(body) > 120 {
						body = body[:120] + "..."
					}
					preview += "\n    " + body
				}
				if len(lines) > 4 {
					preview += "\n    [+" + strconv.Itoa(len(lines)-3) + " more lines]"
				}
				fmt.Printf("  [%02d] %-40s\n    %s\n\n", i, c.SectionTitle, strings.ReplaceAll(preview, "\n", "\n    "))
			}

			// Verify breadcrumb is present on every chunk
			for i, c := range chunks {
				if !strings.HasPrefix(c.Text, "[SOP "+meta.number) {
					t.Errorf("chunk %d missing breadcrumb: %q", i, c.Text[:50])
				}
				if c.SectionTitle == "" {
					t.Errorf("chunk %d has empty SectionTitle", i)
				}
			}
		})
	}
}
