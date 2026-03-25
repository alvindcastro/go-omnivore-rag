// internal/ingest/sop.go
// Phase 2 — SOP metadata parser.
// Extracts SOP number and title from a filename following the convention:
//
//	"SOP 122 Smoke Sanity Tests.docx"
//	"SOP 154 Start Stop Axiom Server.docx"
package ingest

import (
	"path/filepath"
	"regexp"
	"strings"
)

// sopMetadata holds the fields extracted from a SOP filename.
type sopMetadata struct {
	number string // e.g. "122"
	title  string // e.g. "Smoke Sanity Tests"
}

// sopFilenameRegex matches "SOP <number> <title>" case-insensitively.
// Separators between parts may be spaces or underscores.
var sopFilenameRegex = regexp.MustCompile(`(?i)^SOP[\s_]+(\d+)[\s_]+(.+)$`)

// parseSopFilename extracts the SOP number and title from a file path.
// Returns zero-value sopMetadata if the filename does not match the convention.
func parseSopFilename(filePath string) sopMetadata {
	name := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	m := sopFilenameRegex.FindStringSubmatch(name)
	if m == nil {
		return sopMetadata{}
	}
	return sopMetadata{
		number: m[1],
		title:  strings.TrimSpace(m[2]),
	}
}

// isSopDocument reports whether a file path is under the sop input folder.
func isSopDocument(filePath string) bool {
	// Normalise to forward slashes for consistent matching on all platforms.
	normalized := filepath.ToSlash(filePath)
	return strings.Contains(normalized, "/docs/sop/")
}
