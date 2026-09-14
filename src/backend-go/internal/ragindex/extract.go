package ragindex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	fitz "github.com/gen2brain/go-fitz"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/ragstore"
)

// LoadFile reads text from a file based on its extension.
// Mirrors load_file()/load_pdf()/load_md() in the Python indexer.
func LoadFile(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return loadPDF(path)
	case ".md":
		return loadMD(path)
	default:
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}
}

func loadMD(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// loadPDF extracts text from a PDF via go-fitz. It wraps each page's text with
// a "--- Page N ---" separator, matching the Python PyMuPDF behaviour.
//
// This mirrors the Python implementation: it iterates over pages, extracts text,
// and joins with page separators. The Python version returns pages joined with
// newlines; this Go version preserves the same structure so page number
// reconstruction (see pageTextLengths) works identically.
func loadPDF(path string) (string, error) {
	doc, err := fitz.New(path)
	if err != nil {
		return "", err
	}
	defer doc.Close()

	parts := make([]string, 0, doc.NumPage())
	for i := 0; i < doc.NumPage(); i++ {
		text, err := doc.Text(i)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, fmt.Sprintf("--- Page %d ---\n%s", i+1, text))
		}
	}
	return strings.Join(parts, "\n"), nil
}

// chunkText produces overlapping fixed-size chunks of text.
//
// Overlap is applied as follows: after writing a chunk of `size` bytes starting
// at `start`, the next chunk starts `overlap` bytes back from the current chunk's
// end (i.e. `start + size - overlap`). This means consecutive chunks share the
// last `overlap` bytes of the previous one, which keeps context at boundaries
// intact when splitting long documents. The `size` argument is treated as the
// hard upper bound on chunk length, so the final chunk may be shorter than
// `size` but never longer.
//
// If overlap is 0 the behaviour collapses to a plain fixed-size split. If
// overlap >= size the step becomes `size - overlap <= 0`, which would cause an
// infinite loop; we then fall back to stepping by `size` (non-overlapping) so
// the function always terminates and the caller gets usable chunks.
func chunkText(text string, size, overlap int) []string {
	// Guard against non-positive size: nothing to split without a valid size.
	if size <= 0 {
		return nil
	}
	if len(text) <= size {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []string{text}
	}

	var chunks []string
	start := 0
	// Effective step: advance by (size - overlap), clamped so it never goes
	// non-positive. A non-positive step would either loop forever or step
	// backwards, so a minimum of 1 byte keeps the loop alive and terminating.
	step := size - overlap
	if step < 1 {
		step = size
	}

	for start < len(text) {
		end := start + size
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[start:end])
		start += step
	}
	return chunks
}

func documentTitle(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stem = strings.ReplaceAll(stem, "_", " ")
	stem = strings.ReplaceAll(stem, "-", " ")
	return strings.Title(stem)
}

// pageNumbers maps each chunk to the PDF page it starts on. This mirrors the
// Python algorithm: it tracks cumulative page-body text lengths and finds the
// page whose running total the chunk's start offset falls into.
func pageNumbers(text string, numChunks int) []int {
	pageCounts := pageTextLengths(text)
	nums := make([]int, numChunks)
	for i := range nums {
		nums[i] = 0
	}
	if len(pageCounts) == 0 {
		return nums // no per-page info: page 0
	}
	currentPage := 0
	currentPos := 0
	rawChunks := chunkText(text, DefaultChunkSize, DefaultOverlap)
	for i, chunk := range rawChunks {
		for currentPage < len(pageCounts)-1 && currentPos+len(chunk) > pageCounts[currentPage] {
			currentPos += pageCounts[currentPage]
			currentPage++
		}
		if i < len(nums) {
			nums[i] = currentPage + 1
		}
	}
	return nums
}

// pageTextLengths reconstructs per-page body-text lengths from the
// separator-joined text produced by loadPDF. loadPDF joins pages as
// "\n--- Page N ---\n{body}\n", so splitting on the "\n--- Page " separator
// yields one entry per page of the form "N ---\n{body}\n"; we strip the header
// and measure the body length. Page numbers are cosmetic metadata (chunk IDs
// and checksums do not depend on them), so minor off-by-one reconstruction
// differences vs PyMuPDF are acceptable.
func pageTextLengths(text string) []int {
	sep := "\n--- Page "
	if !strings.Contains(text, sep) {
		return nil
	}
	parts := strings.Split(text, sep)
	counts := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if idx := strings.Index(part, "\n"); idx >= 0 {
			body := part[idx+1:]
			if strings.HasSuffix(body, "\n") {
				body = body[:len(body)-1]
			}
			counts = append(counts, len(body))
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

// buildChunks extracts text from a file and produces ragstore.Chunk values,
// replicating build_chunks() in the Python indexer exactly: chunk text,
// page_number, chunk_id (rag-{category}-{stem}-{i:04d}), document_type,
// title, chunk_size, checksum.
func buildChunks(filePath, category string) ([]ragstore.Chunk, error) {
	text, err := LoadFile(filePath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("empty file")
	}

	chunkSize, overlap := DefaultChunkSize, DefaultOverlap
	rawChunks := chunkText(text, chunkSize, overlap)
	if len(rawChunks) == 0 {
		return nil, nil
	}

	stem := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	var pageNumbers_ []int
	if strings.HasSuffix(strings.ToLower(filePath), ".pdf") {
		pageNumbers_ = pageNumbers(text, len(rawChunks))
	} else {
		pageNumbers_ = make([]int, len(rawChunks)) // MD => page 0
	}

	checksum, err := getChecksum(filePath)
	if err != nil {
		return nil, err
	}
	documentType := strings.ToLower(filepath.Ext(filePath))
	title := documentTitle(filePath)

	out := make([]ragstore.Chunk, 0, len(rawChunks))
	for i, chunk := range rawChunks {
		out = append(out, ragstore.Chunk{
			ChunkID:        fmt.Sprintf("rag-%s-%s-%04d", category, stem, i),
			Content:        chunk,
			SourceFile:     filePath,
			SourceCategory: category,
			DocumentType:   documentType,
			Title:          title,
			PageNumber:     pageNumbers_[i],
			ChunkSize:      len(chunk),
			Checksum:       checksum,
		})
	}
	return out, nil
}
