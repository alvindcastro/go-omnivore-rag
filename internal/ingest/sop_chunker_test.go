package ingest

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)


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
