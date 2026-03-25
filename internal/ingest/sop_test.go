// internal/ingest/sop_test.go
// Unit tests for Phase 2 — SOP metadata parser.
package ingest

import "testing"

func TestParseSopFilename(t *testing.T) {
	tests := []struct {
		input  string
		number string
		title  string
	}{
		// Actual filenames from data/docs/sop/
		{
			"SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx",
			"122", "Smoke Test and Sanity Test Post Banner Upgrade",
		},
		{
			"SOP154 - Procedure - Start, Stop Axiom.docx",
			"154", "Procedure - Start, Stop Axiom",
		},
		// Full path — only basename should be used
		{
			"data/docs/sop/SOP200 - Deploy Procedure.docx",
			"200", "Deploy Procedure",
		},
		// Case-insensitive prefix
		{
			"sop10 - lowercase.docx",
			"10", "lowercase",
		},
		// Space between SOP and number
		{
			"SOP 300 - With Space.docx",
			"300", "With Space",
		},
		// Underscore separator
		{
			"SOP_50 - Underscore.docx",
			"50", "Underscore",
		},
		// Leading/trailing whitespace in title stripped
		{
			"SOP99 -   Extra Spaces   .docx",
			"99", "Extra Spaces",
		},
		// Non-matching patterns return empty
		{"not-an-sop.docx", "", ""},
		{"readme.md", "", ""},
		{"Banner_General_9.3.37.pdf", "", ""},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseSopFilename(tt.input)
			if got.number != tt.number {
				t.Errorf("number: got %q, want %q", got.number, tt.number)
			}
			if got.title != tt.title {
				t.Errorf("title: got %q, want %q", got.title, tt.title)
			}
		})
	}
}

func TestIsSopDocument(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Under sop/ folder
		{"data/docs/sop/SOP122 - Smoke Test.docx", true},
		{"data/docs/sop/SOP154 - Start Stop Axiom.docx", true},
		{"/absolute/path/data/docs/sop/file.docx", true},
		// Windows-style paths (backslashes normalised by filepath.ToSlash)
		{`data\docs\sop\SOP154.docx`, true},
		// Not under sop/
		{"data/docs/banner/general/2026/Banner.pdf", false},
		{"data/docs/SOP122.docx", false}, // sop not as a path segment
		{"data/docs/sop-archive/file.docx", false},
		{"sop/file.docx", false}, // missing /docs/ prefix
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isSopDocument(tt.path); got != tt.want {
				t.Errorf("isSopDocument(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
