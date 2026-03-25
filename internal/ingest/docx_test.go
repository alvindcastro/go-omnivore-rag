// internal/ingest/docx_test.go
// Unit tests for Phase 1 — DOCX paragraph extractor.
// Tests parseDocXML directly via io.Reader so no ZIP fixture is needed.
package ingest

import (
	"strings"
	"testing"
)

// minimalDoc wraps body XML in a valid OOXML document element.
func minimalDoc(bodyXML string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body>` + bodyXML + `</w:body></w:document>`
}

func para(style, text string) string {
	var pPr string
	if style != "" && style != "Normal" {
		pPr = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + pPr + `<w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

func numberedPara(text string, level int) string {
	return `<w:p>` +
		`<w:pPr><w:pStyle w:val="ListParagraph"/>` +
		`<w:numPr><w:ilvl w:val="` + strings.Repeat("0", level) + `"/><w:numId w:val="1"/></w:numPr>` +
		`</w:pPr>` +
		`<w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

func TestParseDocXML_BasicStyles(t *testing.T) {
	xml := minimalDoc(
		para("Heading1", "Section One") +
			para("", "Body paragraph.") +
			para("Heading2", "Sub Section"),
	)

	paras, err := parseDocXML(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parseDocXML: %v", err)
	}
	if len(paras) != 3 {
		t.Fatalf("want 3 paragraphs, got %d", len(paras))
	}

	cases := []struct{ style, text string }{
		{"Heading1", "Section One"},
		{"Normal", "Body paragraph."},
		{"Heading2", "Sub Section"},
	}
	for i, c := range cases {
		if paras[i].Style != c.style {
			t.Errorf("para[%d] style: got %q, want %q", i, paras[i].Style, c.style)
		}
		if paras[i].Text != c.text {
			t.Errorf("para[%d] text: got %q, want %q", i, paras[i].Text, c.text)
		}
	}
}

func TestParseDocXML_MultipleRuns(t *testing.T) {
	// Word splits text across multiple <w:r> runs (e.g. bold mid-sentence).
	xml := minimalDoc(
		`<w:p><w:r><w:t>Hello, </w:t></w:r><w:r><w:t>world</w:t></w:r><w:r><w:t>!</w:t></w:r></w:p>`,
	)

	paras, err := parseDocXML(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parseDocXML: %v", err)
	}
	if len(paras) != 1 {
		t.Fatalf("want 1 paragraph, got %d", len(paras))
	}
	if paras[0].Text != "Hello, world!" {
		t.Errorf("text: got %q, want %q", paras[0].Text, "Hello, world!")
	}
}

func TestParseDocXML_EmptyParagraphsDiscarded(t *testing.T) {
	xml := minimalDoc(
		para("Heading1", "Title") +
			`<w:p></w:p>` + // empty — should be discarded
			`<w:p><w:r><w:t>   </w:t></w:r></w:p>` + // whitespace only — discarded
			para("", "Real content"),
	)

	paras, err := parseDocXML(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parseDocXML: %v", err)
	}
	if len(paras) != 2 {
		t.Fatalf("want 2 paragraphs (empty discarded), got %d", len(paras))
	}
}

func TestParseDocXML_NumberedListParagraph(t *testing.T) {
	xml := minimalDoc(numberedPara("Step one", 0))

	paras, err := parseDocXML(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parseDocXML: %v", err)
	}
	if len(paras) != 1 {
		t.Fatalf("want 1 paragraph, got %d", len(paras))
	}
	if !paras[0].IsNumbered {
		t.Error("expected IsNumbered=true")
	}
	if paras[0].Text != "Step one" {
		t.Errorf("text: got %q, want %q", paras[0].Text, "Step one")
	}
}

func TestDocxParagraph_IsHeading(t *testing.T) {
	cases := []struct {
		style string
		want  bool
		level int
	}{
		{"Heading1", true, 1},
		{"Heading2", true, 2},
		{"Heading3", true, 3},
		{"Heading4", true, 4},
		{"Normal", false, 0},
		{"ListParagraph", false, 0},
		{"TOC1", false, 0},
	}
	for _, c := range cases {
		p := DocxParagraph{Style: c.style}
		if p.IsHeading() != c.want {
			t.Errorf("IsHeading(%q) = %v, want %v", c.style, p.IsHeading(), c.want)
		}
		if p.HeadingLevel() != c.level {
			t.Errorf("HeadingLevel(%q) = %d, want %d", c.style, p.HeadingLevel(), c.level)
		}
	}
}

func TestExtractDocxParagraphs_DocFile(t *testing.T) {
	// .doc (legacy binary) must return a clear error, not a ZIP error.
	_, err := extractDocxParagraphs("testdata/fake.doc")
	if err == nil {
		t.Fatal("expected error for .doc file")
	}
	if !strings.Contains(err.Error(), ".doc format is not supported") {
		t.Errorf("unexpected error message: %v", err)
	}
}
