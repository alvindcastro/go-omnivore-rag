// internal/ingest/docx.go
// Phase 1 — DOCX extractor.
// Opens a Word document (.docx), reads word/document.xml from the ZIP archive,
// and returns a slice of DocxParagraph preserving style and numbering metadata.
// No external dependencies — uses only archive/zip and encoding/xml.
package ingest

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// DocxParagraph is a single paragraph from a Word document
// with its style classification and text content.
type DocxParagraph struct {
	Style      string // e.g. "Heading1", "Heading2", "Normal", "ListParagraph"
	Text       string
	IsNumbered bool
	NumLevel   int // 0-based indent level; 0 = top-level list item
}

// IsHeading reports whether the paragraph uses a heading style.
func (p DocxParagraph) IsHeading() bool {
	return strings.HasPrefix(p.Style, "Heading")
}

// HeadingLevel returns the numeric heading level (1–6), or 0 if not a heading.
func (p DocxParagraph) HeadingLevel() int {
	if !p.IsHeading() {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(p.Style, "Heading"))
	return n
}

// extractDocxParagraphs opens a .docx file and returns its body paragraphs.
// Empty paragraphs (whitespace only) are discarded.
// Returns a descriptive error for legacy .doc files — callers should log and skip.
func extractDocxParagraphs(filePath string) ([]DocxParagraph, error) {
	if strings.ToLower(filepath.Ext(filePath)) == ".doc" {
		return nil, fmt.Errorf(".doc format is not supported — please save %q as .docx and re-ingest", filepath.Base(filePath))
	}

	r, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return nil, fmt.Errorf("word/document.xml not found in %s", filePath)
	}

	rc, err := docFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open document.xml: %w", err)
	}
	defer rc.Close()

	return parseDocXML(rc)
}

// parseDocXML reads word/document.xml with a token-based decoder.
// It tracks paragraph state without relying on namespace-aware struct
// unmarshalling, which simplifies handling of OOXML's verbose namespaces.
func parseDocXML(r io.Reader) ([]DocxParagraph, error) {
	dec := xml.NewDecoder(r)

	var (
		paragraphs []DocxParagraph
		cur        *DocxParagraph
		inBody     bool
		inPara     bool
		inRun      bool
		inPPr      bool // inside <w:pPr> paragraph-properties block
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode xml: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "body":
				inBody = true

			case "p":
				if inBody {
					inPara = true
					cur = &DocxParagraph{Style: "Normal"}
				}

			case "pPr":
				if inPara {
					inPPr = true
				}

			case "pStyle":
				if inPPr && cur != nil {
					if v := xmlAttr(t.Attr, "val"); v != "" {
						cur.Style = v
					}
				}

			case "numPr":
				// Presence of <w:numPr> means the paragraph is part of a numbered list.
				if inPPr && cur != nil {
					cur.IsNumbered = true
				}

			case "ilvl":
				if inPPr && cur != nil {
					if v := xmlAttr(t.Attr, "val"); v != "" {
						cur.NumLevel, _ = strconv.Atoi(v)
					}
				}

			case "r":
				if inPara {
					inRun = true
				}

			case "t":
				// <w:t> holds the actual text. DecodeElement reads through </w:t>
				// so the main loop resumes at the next sibling token.
				if inRun && cur != nil {
					var text string
					if err := dec.DecodeElement(&text, &t); err == nil {
						cur.Text += text
					}
				}
			}

		case xml.EndElement:
			switch t.Name.Local {
			case "body":
				inBody = false

			case "p":
				if inPara && cur != nil {
					cur.Text = strings.TrimSpace(cur.Text)
					if cur.Text != "" {
						paragraphs = append(paragraphs, *cur)
					}
					cur = nil
					inPara = false
				}

			case "pPr":
				inPPr = false

			case "r":
				inRun = false
			}
		}
	}

	return paragraphs, nil
}

// xmlAttr returns the value of the first attribute matching localName.
func xmlAttr(attrs []xml.Attr, localName string) string {
	for _, a := range attrs {
		if a.Name.Local == localName {
			return a.Value
		}
	}
	return ""
}
