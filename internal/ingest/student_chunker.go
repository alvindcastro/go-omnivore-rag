// internal/ingest/student_chunker.go
// Section-aware chunker for Banner Student user guide PDFs.
// Heading lines become chunk boundaries; each chunk is prefixed with the
// current heading so section context is embedded in the vector.
package ingest

import (
	"regexp"
	"strings"
)

type sectionChunk struct {
	text    string
	section string
}

// Numbered sections like "3.1 Course Search" or all-caps lines like "COURSE CATALOG".
var studentNumberedSectionRe = regexp.MustCompile(`^\d+(\.\d+)*\s+[A-Z]`)
var allCapsLineRe = regexp.MustCompile(`^[A-Z][A-Z\s\-/,()&]{7,}$`)

func isStudentHeading(line string) bool {
	line = strings.TrimSpace(line)
	if len(line) < 3 || len(line) > 100 {
		return false
	}
	return studentNumberedSectionRe.MatchString(line) || allCapsLineRe.MatchString(line)
}

// chunkStudentText splits a single page's text into section-aware chunks.
// It returns the chunks produced from this page and the updated currentSection
// so the caller can thread section state across pages.
func chunkStudentText(pageText string, currentSection string, chunkSize, overlap int) ([]sectionChunk, string) {
	lines := strings.Split(pageText, "\n")

	var chunks []sectionChunk
	var buf strings.Builder

	flush := func() {
		t := strings.TrimSpace(buf.String())
		if len(t) > 50 {
			chunks = append(chunks, splitWithSection(t, currentSection, chunkSize, overlap)...)
		}
		buf.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isStudentHeading(trimmed) {
			flush()
			currentSection = trimmed
			continue
		}
		if trimmed != "" {
			buf.WriteString(trimmed + " ")
		}
	}
	flush()

	return chunks, currentSection
}

// splitWithSection applies character-based chunking to a section's text and
// prepends the section heading as a breadcrumb to every chunk.
func splitWithSection(text, section string, chunkSize, overlap int) []sectionChunk {
	prefix := ""
	if section != "" {
		prefix = "[" + section + "] "
	}

	effectiveSize := chunkSize - len(prefix)
	if effectiveSize < 100 {
		effectiveSize = 100
	}

	if len(text) <= effectiveSize {
		return []sectionChunk{{text: prefix + text, section: section}}
	}

	raw := chunkText(text, effectiveSize, overlap)
	result := make([]sectionChunk, len(raw))
	for i, r := range raw {
		result[i] = sectionChunk{text: prefix + r, section: section}
	}
	return result
}
