// internal/ingest/ingest.go
// Ingestion pipeline:
//  1. Walk a folder for .pdf, .txt, .md files
//  2. Extract text (page by page for PDFs)
//  3. Split into overlapping chunks
//  4. Embed each chunk via Azure OpenAI
//  5. Upload to Azure AI Search in batches
package ingest

import (
	"crypto/md5"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go-omnivore-rag/config"
	"go-omnivore-rag/internal/azure"

	"github.com/ledongthuc/pdf"
)

// Result summarises what happened during an ingestion run.
type Result struct {
	Status             string `json:"status"`
	DocumentsProcessed int    `json:"documents_processed"`
	ChunksIndexed      int    `json:"chunks_indexed"`
	Message            string `json:"message"`
}

// Run walks docsPath, ingests every supported file, and returns a summary.
func Run(cfg *config.Config, docsPath string, overwrite bool, pagesPerBatch int, startPage int, endPage int) (*Result, error) {
	openai := azure.NewOpenAIClient(cfg)
	search := azure.NewSearchClient(cfg)

	// Optionally recreate the index from scratch
	if overwrite {
		log.Println("Recreating search index...")
		if err := search.CreateIndex(); err != nil {
			return nil, fmt.Errorf("create index: %w", err)
		}
	}

	// Collect supported files
	var files []string
	err := filepath.Walk(docsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && supported[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk docs path: %w", err)
	}

	if len(files) == 0 {
		return &Result{
			Status:  "warning",
			Message: fmt.Sprintf("No supported files found in %s", docsPath),
		}, nil
	}

	totalChunks := 0
	docsProcessed := 0

	for _, filePath := range files {
		log.Printf("📄 Processing: %s", filepath.Base(filePath))

		n, err := ingestFile(filePath, cfg, openai, search, pagesPerBatch, startPage, endPage)
		if err != nil {
			log.Printf("  ✗ Error: %v", err)
			continue
		}

		log.Printf("  ✓ %d chunks indexed", n)
		totalChunks += n
		docsProcessed++
	}

	return &Result{
		Status:             "success",
		DocumentsProcessed: docsProcessed,
		ChunksIndexed:      totalChunks,
		Message: fmt.Sprintf(
			"Ingested %d documents (%d chunks) into %q",
			docsProcessed, totalChunks, cfg.AzureSearchIndexName,
		),
	}, nil
}

// ─── File Processing ──────────────────────────────────────────────────────────

func ingestFile(
	filePath string,
	cfg *config.Config,
	openai *azure.OpenAIClient,
	search *azure.SearchClient,
	pagesPerBatch int,
	startPage int,
	endPage int,
) (int, error) {
	filename := filepath.Base(filePath)
	meta := parseMetadata(filePath)

	pages, err := extractPages(filePath)
	if err != nil {
		return 0, fmt.Errorf("extract pages: %w", err)
	}
	if len(pages) == 0 {
		return 0, nil
	}

	// Filter pages by range if specified
	if startPage > 0 || endPage > 0 {
		var filtered []pageContent
		for _, p := range pages {
			if startPage > 0 && p.pageNum < startPage {
				continue
			}
			if endPage > 0 && p.pageNum > endPage {
				continue
			}
			filtered = append(filtered, p)
		}
		pages = filtered
		log.Printf("  Page range filter: %d-%d — %d pages selected", startPage, endPage, len(pages))
	}

	log.Printf("  Total pages to process: %d — batch size: %d", len(pages), pagesPerBatch)

	totalChunks := 0
	batchSize := pagesPerBatch

	for i := 0; i < len(pages); i += batchSize {
		end := i + batchSize
		if end > len(pages) {
			end = len(pages)
		}

		batch := pages[i:end]
		log.Printf("  Processing pages %d-%d of %d...", pages[i].pageNum, pages[end-1].pageNum, len(pages))

		var docs []azure.ChunkDocument
		chunkIndex := 0

		for _, page := range batch {
			chunks := chunkText(page.text, cfg.ChunkSize, cfg.ChunkOverlap)
			log.Printf("    Page %d produced %d chunks", page.pageNum, len(chunks))
			for _, chunk := range chunks {
				chunk = sanitizeText(chunk)
				if chunk == "" {
					continue
				}

				log.Printf("    Embedding chunk %d (page %d, %d chars)...", chunkIndex, page.pageNum, len(chunk))

				vector, err := openai.EmbedText(chunk)
				if err != nil {
					log.Printf("    ⚠ Skipping chunk %d — error: %v", chunkIndex, err)
					chunkIndex++
					continue
				}
				// Small pause between API calls to avoid rate limiting
				time.Sleep(500 * time.Millisecond)
				docs = append(docs, azure.ChunkDocument{
					ID:            chunkID(filename, page.pageNum, chunkIndex),
					Filename:      filename,
					PageNumber:    page.pageNum,
					BannerModule:  meta.module,
					BannerVersion: meta.version,
					Year:          meta.year,
					ChunkText:     chunk,
					ContentVector: vector,
				})
				chunkIndex++
			}
		}

		// Upload this batch to Azure AI Search
		if len(docs) > 0 {
			log.Printf("  Uploading %d chunks to Azure Search...", len(docs))
			for j := 0; j < len(docs); j += 100 {
				batchEnd := j + 100
				if batchEnd > len(docs) {
					batchEnd = len(docs)
				}
				if err := search.UploadDocuments(docs[j:batchEnd]); err != nil {
					return totalChunks, fmt.Errorf("upload batch: %w", err)
				}
			}
			totalChunks += len(docs)
			log.Printf("  ✓ Done — %d chunks uploaded (total so far: %d)", len(docs), totalChunks)
		}
	}

	return totalChunks, nil
}

// ─── Text Extraction ──────────────────────────────────────────────────────────

type pageContent struct {
	pageNum int
	text    string
}

func extractPages(filePath string) ([]pageContent, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".pdf":
		return extractPDFPages(filePath)
	case ".txt", ".md":
		return extractTextFile(filePath)
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
}

func extractPDFPages(filePath string) ([]pageContent, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	var pages []pageContent
	numPages := r.NumPage()

	for i := 1; i <= numPages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			log.Printf("  Warning: could not extract text from page %d: %v", i, err)
			continue
		}
		text = sanitizeText(text)
		if text != "" {
			pages = append(pages, pageContent{pageNum: i, text: text})
		}
	}
	return pages, nil
}

func extractTextFile(filePath string) ([]pageContent, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, nil
	}
	return []pageContent{{pageNum: 1, text: text}}, nil
}

// ─── Chunking ─────────────────────────────────────────────────────────────────

// chunkText splits text into overlapping character-based chunks,
// trying to break on paragraph or sentence boundaries.
func chunkText(text string, chunkSize, overlap int) []string {
	text = strings.TrimSpace(text)
	if len(text) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	start := 0

	for start < len(text) {
		end := start + chunkSize
		if end >= len(text) {
			chunk := strings.TrimSpace(text[start:])
			if len(chunk) > 50 { // skip tiny trailing chunks
				chunks = append(chunks, chunk)
			}
			break
		}

		// Try to find a clean break point working backwards from end
		breakAt := end
		for _, sep := range []string{"\n\n", "\n", ". ", "? ", "! ", ", ", " "} {
			pos := strings.LastIndex(text[start:end], sep)
			if pos > chunkSize/2 { // break must be in second half of chunk
				breakAt = start + pos + len(sep)
				break
			}
		}

		chunk := strings.TrimSpace(text[start:breakAt])
		if len(chunk) > 50 { // skip tiny chunks
			chunks = append(chunks, chunk)
		}

		// Move forward by chunkSize minus overlap
		start = breakAt - overlap
		if start <= 0 {
			start = breakAt // no overlap at beginning
		}
	}

	return chunks
}

// ─── Metadata ────────────────────────────────────────────────────────────────

type docMetadata struct {
	module  string
	version string
	year    string
}

var knownModules = []string{
	"Finance", "Student", "HR", "Human_Resources",
	"Financial_Aid", "General", "Advancement", "Payroll",
	"Accounts_Receivable", "Position_Control",
}

var versionRegex = regexp.MustCompile(`(\d+\.\d+\.\d+(?:\.\d+)?)`)
var yearRegex = regexp.MustCompile(`\b(20\d{2})\b`)

// parseMetadata extracts Banner module and version from the filename.
// Example: Banner_Finance_9.3.22_ReleaseNotes.pdf → {Finance, 9.3.22}
func parseMetadata(filePath string) docMetadata {
	filename := filepath.Base(filePath)
	lowerPath := strings.ToLower(filePath)
	meta := docMetadata{}

	// Detect module from folder name
	for _, mod := range knownModules {
		if strings.Contains(lowerPath, strings.ToLower(mod)) {
			meta.module = strings.ReplaceAll(mod, "_", " ")
			break
		}
	}

	// Detect version from filename
	matches := versionRegex.FindAllString(filename, -1)
	log.Printf("  Version matches found in filename: %v", matches) // add this
	for _, v := range matches {
		if !strings.HasPrefix(v, "20") {
			meta.version = v
			break
		}
	}

	// Detect year from folder path
	yearMatches := yearRegex.FindAllString(filePath, -1)
	if len(yearMatches) > 0 {
		meta.year = yearMatches[0]
	}

	log.Printf("  Metadata — module: %q, version: %q, year: %q", meta.module, meta.version, meta.year)
	return meta
}

// chunkID generates a deterministic ID for a chunk.
func chunkID(filename string, page, index int) string {
	raw := fmt.Sprintf("%s::p%d::c%d", filename, page, index)
	return fmt.Sprintf("%x", md5.Sum([]byte(raw)))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sanitizeText(text string) string {
	// Replace common PDF special characters
	replacer := strings.NewReplacer(
		"•", "-",
		"–", "-",
		"—", "-",
		"\u00a0", " ",
		"\u200b", "",
		"\ufffd", "",
		"\f", " ",
		"\r", " ",
	)
	text = strings.TrimSpace(replacer.Replace(text))

	// Trim mid-word fragments from the start
	// A fragment is any leading text before the first space that is
	// shorter than 5 chars AND not a known short word
	firstSpace := strings.Index(text, " ")
	if firstSpace > 0 && firstSpace <= 5 {
		firstWord := text[:firstSpace]
		if !isCommonShortWord(firstWord) {
			text = strings.TrimSpace(text[firstSpace:])
		}
	}

	return text
}

func isCommonShortWord(w string) bool {
	w = strings.ToLower(w)
	short := map[string]bool{
		"a": true, "an": true, "the": true, "in": true,
		"on": true, "at": true, "to": true, "of": true,
		"or": true, "is": true, "it": true, "as": true,
		"by": true, "be": true, "if": true, "no": true,
	}
	return short[w]
}
