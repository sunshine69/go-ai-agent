// Package ragindex is a Go port of src/backend/app/cli/rag_indexer.py.
//
// It walks RAG_DOCS_DIR (repo root / resources/rag_documents), extracts text
// from .md and .pdf files (using go-fitz for PDFs), chunks the text with a
// fixed size + overlap, computes SHA256 checksums and chunk IDs, persists a
// state JSON sidecar, and hands the resulting chunks to a ragstore.RAGStore
// for embedding + insertion.
//
// The chunking, chunk-id format and state schema mirror the Python indexer so
// the two backends stay interchangeable. Page-number mapping is replicated
// from the Python algorithm (per-page body-text offsets against the full
// separator-joined text).
package ragindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stevek/go-ai-agent/backend-go/internal/ragstore"
)

// Default chunking params, matching RAG_CHUNK_SIZE / RAG_CHUNK_OVERLAP in the
// Python indexer. These are used as the fallback values in New() when a
// non-positive chunk size or overlap is supplied (i.e. unset config).
const (
	DefaultChunkSize = 512
	DefaultOverlap   = 50
)

// Mode selects the indexing strategy.
type Mode string

const (
	ModeFull       Mode = "full"
	ModeIncremental Mode = "incremental"
	ModeReset      Mode = "reset"
	ModeDryRun     Mode = "dry-run"
)

// StateKey / indexState mirror the Python _index_state.json schema.
type StateKey struct {
	Checksum    string   `json:"checksum"`
	LastIndexed string   `json:"last_indexed"`
	Chunks      []string `json:"chunks"`
}
type indexState map[string]StateKey

// Options configures the indexer.
type Options struct {
	DocsDir    string
	ChunkSize  int
	ChunkOverlap int
	Category   string // if set, only that category dir is walked
	Mode       Mode
	Verbose    bool
	Force      bool // ignore state; re-index everything
}

// Indexer holds the loaded state and the target store.
type Indexer struct {
	opts      Options
	state     indexState
	statePath string
	store     *ragstore.RAGStore

	// counters for summaries
	totalFiles, totalNew, totalUpdated, totalSkipped int
}

// New creates an indexer bound to a ragstore. The state file (if any) is loaded.
func New(store *ragstore.RAGStore, opts Options) (*Indexer, error) {
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = DefaultChunkSize
	}
	if opts.ChunkOverlap <= 0 {
		opts.ChunkOverlap = DefaultOverlap
	}
	if opts.DocsDir == "" {
		opts.DocsDir = "resources/rag_documents"
	}
	if opts.Mode == "" {
		opts.Mode = ModeIncremental
	}
	ix := &Indexer{
		opts:      opts,
		state:     indexState{},
		statePath: filepath.Join(opts.DocsDir, "_index_state.json"),
		store:     store,
	}
	if err := ix.loadState(); err != nil {
		return nil, err
	}
	return ix, nil
}

func (ix *Indexer) loadState() error {
	data, err := os.ReadFile(ix.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &ix.state); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	return nil
}

func (ix *Indexer) saveState() error {
	data, err := json.MarshalIndent(ix.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	return os.WriteFile(ix.statePath, data, 0o644)
}

// GetState returns a snapshot of the current index state.
func (ix *Indexer) GetState() indexState {
	out := make(indexState, len(ix.state))
	for k, v := range ix.state {
		out[k] = v
	}
	return out
}

// Stats summarizes a run.
type Stats struct {
	TotalFiles   int
	TotalNew     int
	TotalUpdated int
	TotalSkipped int
}

// Run executes the indexing loop for the configured mode.
func (ix *Indexer) Run() error {
	// Reset mode: drop the collection AND the state file first. This is
	// the core fix — without clearing state, incrementally-mode would
	// skip everything on the next run.
	if ix.opts.Mode == ModeReset {
		fmt.Println("[RESET] Deleting existing collection and state file...")
		if err := ix.store.DeleteCollection(context.Background()); err != nil {
			return fmt.Errorf("reset delete: %w", err)
		}
		ix.state = indexState{}
		if err := os.Remove(ix.statePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove state: %w", err)
		}
		fmt.Printf("[RESET] state file %s removed\n", ix.statePath)
	}

	// Determine categories.
	var categories []string
	if ix.opts.Category != "" {
		categories = []string{ix.opts.Category}
	} else {
		entries, err := os.ReadDir(ix.opts.DocsDir)
		if err != nil {
			return fmt.Errorf("read docs dir: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				categories = append(categories, e.Name())
			}
		}
	}
	if len(categories) == 0 {
		fmt.Println("No categories found in rag_documents directory")
		return nil
	}
	fmt.Printf("Categories to index: %s\n\n", strings.Join(categories, ", "))

	for _, cat := range categories {
		if err := ix.indexCategory(cat); err != nil {
			return fmt.Errorf("index category %q: %w", cat, err)
		}
	}

	if err := ix.saveState(); err != nil {
		return err
	}

	s := ix.summary()
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("Indexing Summary")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Total files: %d\n", s.TotalFiles)
	fmt.Printf("New chunks: %d\n", s.TotalNew)
	fmt.Printf("Updated: %d\n", s.TotalUpdated)
	fmt.Printf("Skipped: %d\n", s.TotalSkipped)

	if ix.opts.Mode != ModeDryRun {
		stats, err := ix.store.GetStats(context.Background())
		if err != nil {
			return err
		}
		fmt.Printf("Total in store: %d\n", stats.TotalChunks)
		fmt.Printf("Categories: %s\n", strings.Join(stats.Categories, ", "))
	}
	return nil
}

func (ix *Indexer) summary() Stats {
	return Stats{
		TotalFiles:   ix.totalFiles,
		TotalNew:     ix.totalNew,
		TotalUpdated: ix.totalUpdated,
		TotalSkipped: ix.totalSkipped,
	}
}

func (ix *Indexer) indexCategory(cat string) error {
	fmt.Printf("\n%s\n%s\n", strings.Repeat("=", 40), "Category: "+cat)
	catDir := filepath.Join(ix.opts.DocsDir, cat)
	if _, err := os.Stat(catDir); err != nil {
		fmt.Printf("  Category directory not found: %s\n", catDir)
		return nil
	}

	err := filepath.Walk(catDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".pdf" {
			return nil
		}
		ix.handleFile(path, cat)
		return nil
	})
	return err
}

// handleFile indexes a single file, honoring incremental/skip logic.
func (ix *Indexer) handleFile(path, category string) {
	ix.totalFiles++
	relPath := ix.rel(path, category)

	if ix.opts.Verbose {
		fmt.Printf("  [DBG] processing %s\n", relPath)
	}

	checksum, err := getChecksum(path)
	if err != nil {
		fmt.Printf("  ERROR checksum %s: %v\n", relPath, err)
		return
	}

	stateKey := fmt.Sprintf("%s/%s", category, relPath)
	oldChecksum := ""
	if existing, ok := ix.state[stateKey]; ok {
		oldChecksum = existing.Checksum
	}

	// Decide skip: skip only if incremental mode AND checksum unchanged AND
	// NOT forced.
	if !ix.opts.Force && oldChecksum == checksum {
		if ix.opts.Mode == ModeIncremental {
			fmt.Printf("  SKIP: %s (unchanged)\n", relPath)
			ix.totalSkipped++
			return
		}
		// full/reset mode: even if unchanged, re-index (state was cleared for
		// reset; full always re-adds all chunks).
		if ix.opts.Verbose {
			fmt.Printf("  [DBG] %s checksum unchanged but mode=%s: re-indexing\n", relPath, ix.opts.Mode)
		}
	} else if ix.opts.Verbose {
		if oldChecksum == "" {
			fmt.Printf("  [DBG] %s NEW file (no prior state)\n", relPath)
		} else if ix.opts.Force {
			fmt.Printf("  [DBG] %s force re-index (checksum was %s)\n", relPath, oldChecksum[:min(len(oldChecksum), 8)])
		} else {
			fmt.Printf("  [DBG] %s changed (was %s, now %s)\n", relPath, oldChecksum[:min(len(oldChecksum), 8)], checksum[:min(len(checksum), 8)])
		}
	}

	fmt.Printf("  Parsing: %s\n", relPath)

	// Read file text.
	text, err := LoadFile(path)
	if err != nil {
		fmt.Printf("    ERROR loading file: %v\n", err)
		return
	}
	if strings.TrimSpace(text) == "" {
		fmt.Printf("    WARNING: empty file (no text extracted) — skipping\n")
		ix.totalSkipped++
		return
	}

	// Use the configured chunk size / overlap (from RAG_CHUNK_SIZE /
	// RAG_CHUNK_OVERLAP, validated in New()). The Default* constants are kept
	// only for the fallback in New().
	chunkSize, overlap := ix.opts.ChunkSize, ix.opts.ChunkOverlap
	rawChunks := chunkText(text, chunkSize, overlap)
	if len(rawChunks) == 0 {
		fmt.Printf("    WARNING: No chunks generated\n")
		ix.totalSkipped++
		return
	}

	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	var pageNumbers_ []int
	if strings.HasSuffix(strings.ToLower(path), ".pdf") {
		pageNumbers_ = pageNumbers(text, len(rawChunks))
	} else {
		pageNumbers_ = make([]int, len(rawChunks)) // MD => page 0
	}

	documentType := strings.ToLower(filepath.Ext(path))
	title := documentTitle(path)

	chunks := make([]ragstore.Chunk, 0, len(rawChunks))
	for i, chunk := range rawChunks {
		chunks = append(chunks, ragstore.Chunk{
			ChunkID:        fmt.Sprintf("rag-%s-%s-%04d", category, stem, i),
			Content:        chunk,
			SourceFile:     path,
			SourceCategory: category,
			DocumentType:   documentType,
			Title:          title,
			PageNumber:     pageNumbers_[i],
			ChunkSize:      len(chunk),
			Checksum:       checksum,
		})
	}

	if ix.opts.Mode == ModeDryRun {
		fmt.Printf("    DRY-RUN: %d chunks would be created\n", len(chunks))
		ix.totalNew += len(chunks)
		ix.updateState(stateKey, checksum, chunkIDs(chunks))
		return
	}

	// Incremental: only add new chunks (by chunk_id) to the store.
	if ix.opts.Mode == ModeIncremental && oldChecksum != "" {
		oldIDs := map[string]bool{}
		if existing := ix.state[stateKey]; existing.Chunks != nil {
			for _, id := range existing.Chunks {
				oldIDs[id] = true
			}
		}
		newChunks := make([]ragstore.Chunk, 0, len(chunks))
		for _, c := range chunks {
			if !oldIDs[c.ChunkID] {
				newChunks = append(newChunks, c)
			}
		}
		if len(newChunks) > 0 {
			if _, err := ix.store.AddDocuments(context.Background(), newChunks); err != nil {
				fmt.Printf("    ERROR adding chunks: %v\n", err)
				return
			}
			ix.totalNew += len(newChunks)
			fmt.Printf("    Adding: %d new chunks\n", len(newChunks))
		} else {
			ix.totalUpdated++
			fmt.Printf("    UPDATING: Metadata only\n")
		}
	} else {
		// New file or full mode: add all chunks.
		if _, err := ix.store.AddDocuments(context.Background(), chunks); err != nil {
			fmt.Printf("    ERROR adding chunks: %v\n", err)
			return
		}
		ix.totalNew += len(chunks)
		fmt.Printf("    Adding: %d chunks\n", len(chunks))
	}

	ix.updateState(stateKey, checksum, chunkIDs(chunks))
}

func (ix *Indexer) updateState(stateKey, checksum string, ids []string) {
	ix.state[stateKey] = StateKey{
		Checksum:    checksum,
		LastIndexed: time.Now().Format(time.RFC3339Nano),
		Chunks:      ids,
	}
}

// --- helpers ---

func (ix *Indexer) rel(path, category string) string {
	// Relativize the file path to its category directory
	// (e.g. "dhm/about.md").
	catDir := filepath.Join(ix.opts.DocsDir, category)
	rel, err := filepath.Rel(catDir, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func chunkIDs(chunks []ragstore.Chunk) []string {
	ids := make([]string, len(chunks))
	for i, c := range chunks {
		ids[i] = c.ChunkID
	}
	return ids
}

func getChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
