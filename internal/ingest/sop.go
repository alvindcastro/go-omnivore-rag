// internal/ingest/sop.go
// Phase 2 — SOP metadata parser.
// Extracts SOP number and title from a filename following the convention:
//
//	"SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx"
//	"SOP154 - Procedure - Start, Stop Axiom.docx"
package ingest

import (
	"path/filepath"
	"regexp"
	"strings"
)

// sopMetadata holds the fields extracted from a SOP filename.
type sopMetadata struct {
	number string // e.g. "122"
	title  string // e.g. "Smoke Test and Sanity Test Post Banner Upgrade"
}

// sopFilenameRegex matches "SOP<number> - <title>" case-insensitively.
// The number may be separated from "SOP" by an optional space or underscore,
// and the title is separated by a dash (with surrounding whitespace).
var sopFilenameRegex = regexp.MustCompile(`(?i)^SOP[\s_]*(\d+)\s*-\s*(.+)$`)

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
