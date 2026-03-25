package ingest

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
