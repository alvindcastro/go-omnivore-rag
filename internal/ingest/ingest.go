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

	"go-banner-rag/config"
	"go-banner-rag/internal/azure"

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
func Run(cfg *config.Config, docsPath string, overwrite bool) (*Result, error) {
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
	supported := map[string]bool{".pdf": true, ".txt": true, ".md": true}
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

		n, err := ingestFile(filePath, cfg, openai, search)
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

	var docs []azure.ChunkDocument
	chunkIndex := 0

	for _, page := range pages {
		chunks := chunkText(page.text, cfg.ChunkSize, cfg.ChunkOverlap)
		for _, chunk := range chunks {
			log.Printf("  Embedding chunk %d...", chunkIndex)
			vector, err := openai.EmbedText(chunk)
			if err != nil {
				return 0, fmt.Errorf("embed chunk: %w", err)
			}

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

	// Upload in batches of 100
	for i := 0; i < len(docs); i += 100 {
		end := i + 100
		if end > len(docs) {
			end = len(docs)
		}
		if err := search.UploadDocuments(docs[i:end]); err != nil {
			return 0, fmt.Errorf("upload batch: %w", err)
		}
	}

	return len(docs), nil
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
		text = strings.TrimSpace(text)
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
	if len(text) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	start := 0

	for start < len(text) {
		end := start + chunkSize
		if end >= len(text) {
			chunk := strings.TrimSpace(text[start:])
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}

		// Try to find a clean break point
		breakAt := end
		for _, sep := range []string{"\n\n", "\n", ". ", " "} {
			pos := strings.LastIndex(text[start:end], sep)
			if pos > 0 {
				breakAt = start + pos + len(sep)
				break
			}
		}

		chunk := strings.TrimSpace(text[start:breakAt])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		start = breakAt - overlap
		if start < 0 {
			start = 0
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

var versionRegex = regexp.MustCompile(`\b(\d+\.\d+(?:\.\d+)?)\b`)
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

	// Detect version from filename (e.g. 9.3.22)
	matches := versionRegex.FindAllString(filename, -1)
	for _, v := range matches {
		if !strings.HasPrefix(v, "20") {
			meta.version = v
			break
		}
	}

	// Detect year from folder path (e.g. 2026)
	yearMatches := yearRegex.FindAllString(filePath, -1)
	if len(yearMatches) > 0 {
		meta.year = yearMatches[0]
	}

	return meta
}

// chunkID generates a deterministic ID for a chunk.
func chunkID(filename string, page, index int) string {
	raw := fmt.Sprintf("%s::p%d::c%d", filename, page, index)
	return fmt.Sprintf("%x", md5.Sum([]byte(raw)))
}
