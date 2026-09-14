// Package ragstore is a Go port of the Python RAGStore: a SQLite + sqlite-vec
// backed vector store for RAG documents.
//
// This variant uses the CGO path: mattn/go-sqlite3 as the driver, with
// sqlite-vec compiled from source and statically linked in via
// asg017/sqlite-vec-go-bindings/cgo (sqlite_vec.Auto()).
//
// Build requirements: CGO_ENABLED=1 and a C toolchain for every target
// platform. Native per-OS CI builds (windows-latest/ubuntu-latest/
// macos-latest) need nothing extra. Cross-compiling a Windows binary from
// Linux/Mac needs mingw-w64 wired into CC (e.g.
// CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build).
//
// go.mod requires:
//
//	github.com/mattn/go-sqlite3
//	github.com/asg017/sqlite-vec-go-bindings
//
// NOTE: same caveat as the modernc version — this sandbox's network
// allowlist doesn't include the Go module proxy or github.com's git
// endpoints needed for `go mod download`, so this was gofmt-verified for
// syntax only, not actually go build'd against the real module. Flag
// anything that doesn't build and I'll fix it.
package ragstore

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/config"
)

// sqlite_vec.Auto() registers a ConnectHook on the driver; it must run
// exactly once per process, before the first sql.Open.
var registerVecOnce sync.Once

// Embedder produces vector embeddings for text. Implement this against
// whatever you use in place of sentence-transformers — e.g. an HTTP call to
// a local Ollama embedding model, an ONNX runtime binding, or a hosted
// embeddings API.
type Embedder interface {
	// Embed returns one embedding vector per input text, same order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dimensions reports the embedding size (e.g. 384 for all-MiniLM-L6-v2).
	Dimensions() int
}

// Config mirrors the Python module's env-var configuration.
type Config struct {
	Enabled        bool
	DBPath         string
	ChunkSize      int
	ChunkOverlap   int
	SearchLimit    int
	ScoreThreshold float64
}

// ConfigFromEnv returns a RAG config populated from environment variables.
func ConfigFromEnv() Config {
	return Config{
		Enabled:        getEnvBool("RAG_ENABLED", true),
		DBPath:         getEnv("RAG_SQLITE_PATH", "./rag.db"),
		ChunkSize:      getEnvInt("RAG_CHUNK_SIZE", 1500),
		ChunkOverlap:   getEnvInt("RAG_CHUNK_OVERLAP", 300),
		SearchLimit:    getEnvInt("RAG_SEARCH_LIMIT", 5),
		ScoreThreshold: getEnvFloat("RAG_SCORE_THRESHOLD", 0.25),
	}
}

// ConfigFromConfigMap returns a RAG config populated from the backend-go
// internal/config.Config fields.
func ConfigFromConfigMap(cfg *config.Config) Config {
	return Config{
		Enabled:        cfg.RAGEnabled,
		DBPath:         cfg.RAGDBPath,
		ChunkSize:      cfg.RAGChunkSize,
		ChunkOverlap:   cfg.RAGChunkOverlap,
		SearchLimit:    cfg.RAGSearchLimit,
		ScoreThreshold: cfg.RAGScoreThreshold,
	}
}

// Chunk is one document chunk, in and out of the store.
type Chunk struct {
	ChunkID        string
	Content        string
	SourceFile     string
	SourceCategory string
	DocumentType   string
	Title          string
	PageNumber     int
	ChunkSize      int
	Checksum       string
	LastIndexed    time.Time
}

// SearchResult is one scored hit.
type SearchResult struct {
	ChunkID         string
	DocumentTitle   string
	SourceFile      string
	SourceCategory  string
	PageNumber      int
	Content         string
	SimilarityScore float64
}

// Stats mirrors get_stats().
type Stats struct {
	TotalChunks    int
	Categories     []string
	CategoryCounts map[string]int
	Dimensions     int
	Disabled       bool
}

// RAGStore is the Go equivalent of the Python RAGStore class.
type RAGStore struct {
	cfg      Config
	embedder Embedder
	db       *sql.DB
	disabled bool
}

// New opens (or creates) the SQLite-backed store. When cfg.Enabled is false
// it behaves as a no-op, same as the Python version's disabled mode — pass
// a nil embedder in that case.
func New(cfg Config, embedder Embedder) (*RAGStore, error) {
	if !cfg.Enabled {
		return &RAGStore{cfg: cfg, disabled: true}, nil
	}
	if embedder == nil {
		return nil, errors.New("ragstore: embedder is required when RAG is enabled")
	}

	registerVecOnce.Do(func() {
		sqlite_vec.Auto()
	})

	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000", cfg.DBPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // sqlite: keep writes serialized; matches Chroma's single-writer behavior

	s := &RAGStore{cfg: cfg, embedder: embedder, db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *RAGStore) migrate() error {
	dim := s.embedder.Dimensions()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chunks (
			chunk_id        TEXT PRIMARY KEY,
			source_file     TEXT,
			source_category TEXT,
			document_type   TEXT,
			title           TEXT,
			page_number     INTEGER,
			chunk_size      INTEGER,
			last_indexed    TEXT,
			checksum        TEXT,
			content         TEXT
		)`,
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_chunks USING vec0(
			chunk_id  TEXT PRIMARY KEY,
			embedding FLOAT[%d] distance_metric=cosine
		)`, dim),
		`CREATE INDEX IF NOT EXISTS idx_chunks_category ON chunks(source_category)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
	}
	return nil
}

// Search performs a vector similarity search, optionally filtered by category.
func (s *RAGStore) Search(ctx context.Context, query string, limit int, category string) ([]SearchResult, error) {
	if s.disabled {
		return nil, nil
	}
	if limit <= 0 {
		limit = s.cfg.SearchLimit
	}

	embeddings, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, errors.New("embed query returned no vectors")
	}
	queryBlob := serializeFloat32(embeddings[0])
	fmt.Printf("[DBG-RAG] Search: dim=%d k=%d threshold=%.4f query=%q category=%q embedderDim=%d\n",
		len(embeddings[0]), limit*4, s.cfg.ScoreThreshold, query, category, s.embedder.Dimensions())

	// Over-fetch candidates by vector distance, then join + filter by
	// category and threshold in SQL. k is generous (limit * 4, min 20) to
	// absorb rows that get filtered out post-join.
	k := limit * 4
	if k < 20 {
		k = 20
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.chunk_id, c.title, c.source_file, c.source_category,
		       c.page_number, c.content, v.distance
		FROM vec_chunks v
		JOIN chunks c ON c.chunk_id = v.chunk_id
		WHERE v.embedding MATCH ? AND v.k = ?
		  AND (? = '' OR c.source_category = ?)
		ORDER BY v.distance
	`, queryBlob, k, category, category)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	// Count raw rows from vec0 (before threshold) so we can see if the
	// embedding query is returning candidates at all.
	var rawMatched, overThreshold int
	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var distance float64
		if err := rows.Scan(&r.ChunkID, &r.DocumentTitle, &r.SourceFile,
			&r.SourceCategory, &r.PageNumber, &r.Content, &distance); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		rawMatched++
		// cosine distance from sqlite-vec is in [0, 2]; same conversion as
		// the Python version.
		r.SimilarityScore = 1.0 - (distance / 2.0)
		fmt.Printf("[DBG-RAG] Search: candidate #%d score=%.4f title=%q file=%q category=%q page=%d\n",
			rawMatched, r.SimilarityScore, r.DocumentTitle, r.SourceFile, r.SourceCategory, r.PageNumber)
		if r.SimilarityScore < s.cfg.ScoreThreshold {
			continue
		}
		overThreshold++
		results = append(results, r)
		if len(results) >= limit {
			break
		}
	}
	if rawMatched == 0 {
		fmt.Printf("[DBG-RAG] Search: NO candidate rows returned from vec0 MATCH (db likely empty or dim mismatch) — dbPath=%s\n", s.cfg.DBPath)
	} else {
		fmt.Printf("[DBG-RAG] Search: %d raw candidates, %d passed threshold=%.4f, %d returned\n", rawMatched, overThreshold, s.cfg.ScoreThreshold, len(results))
	}
	return results, rows.Err()
}

// AddDocuments embeds and inserts chunks, batching the embedding call.
func (s *RAGStore) AddDocuments(ctx context.Context, chunks []Chunk) (int, error) {
	if s.disabled || len(chunks) == 0 {
		return 0, nil
	}

	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	embeddings, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed batch: %w", err)
	}
	if len(embeddings) != len(chunks) {
		return 0, fmt.Errorf("embedder returned %d vectors for %d chunks", len(embeddings), len(chunks))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	insertChunk, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks (chunk_id, source_file, source_category, document_type,
			title, page_number, chunk_size, last_indexed, checksum, content)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chunk_id) DO UPDATE SET
			source_file=excluded.source_file, source_category=excluded.source_category,
			document_type=excluded.document_type, title=excluded.title,
			page_number=excluded.page_number, chunk_size=excluded.chunk_size,
			last_indexed=excluded.last_indexed, checksum=excluded.checksum,
			content=excluded.content
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare chunk insert: %w", err)
	}
	defer insertChunk.Close()

	insertVec, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO vec_chunks (chunk_id, embedding) VALUES (?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare vec insert: %w", err)
	}
	defer insertVec.Close()

	now := time.Now().Format(time.RFC3339)
	for i, c := range chunks {
		if _, err := insertChunk.ExecContext(ctx, c.ChunkID, c.SourceFile, c.SourceCategory,
			c.DocumentType, c.Title, c.PageNumber, c.ChunkSize, now, c.Checksum, c.Content); err != nil {
			return 0, fmt.Errorf("insert chunk %s: %w", c.ChunkID, err)
		}
		if _, err := insertVec.ExecContext(ctx, c.ChunkID, serializeFloat32(embeddings[i])); err != nil {
			return 0, fmt.Errorf("insert vec %s: %w", c.ChunkID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(chunks), nil
}

// GetCategories returns all distinct source categories.
func (s *RAGStore) GetCategories(ctx context.Context) ([]string, error) {
	if s.disabled {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT source_category FROM chunks WHERE source_category != '' ORDER BY source_category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cats []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cats = append(cats, c)
	}
	return cats, rows.Err()
}

// GetStats mirrors get_stats().
func (s *RAGStore) GetStats(ctx context.Context) (Stats, error) {
	if s.disabled {
		return Stats{Disabled: true}, nil
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&total); err != nil {
		return Stats{}, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT source_category, COUNT(*) FROM chunks WHERE source_category != '' GROUP BY source_category`)
	if err != nil {
		return Stats{}, err
	}
	defer rows.Close()

	counts := map[string]int{}
	var cats []string
	for rows.Next() {
		var cat string
		var n int
		if err := rows.Scan(&cat, &n); err != nil {
			return Stats{}, err
		}
		counts[cat] = n
		cats = append(cats, cat)
	}

	return Stats{
		TotalChunks:    total,
		Categories:     cats,
		CategoryCounts: counts,
		Dimensions:     s.embedder.Dimensions(),
	}, rows.Err()
}

// DeleteCollection drops and recreates both tables (reset mode).
func (s *RAGStore) DeleteCollection(ctx context.Context) error {
	if s.disabled {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS chunks`); err != nil {
		return fmt.Errorf("drop chunks: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS vec_chunks`); err != nil {
		return fmt.Errorf("drop vec_chunks: %w", err)
	}
	return s.migrate()
}

func (s *RAGStore) Close() error {
	if s.disabled || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// serializeFloat32 encodes a []float32 into the little-endian BLOB format
// sqlite-vec expects (equivalent to sqlite_vec.SerializeFloat32 in the
// asg017 Go bindings — reimplemented here to avoid an extra import).
func serializeFloat32(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// --- env helpers, mirroring the Python os.getenv(...) defaults ---

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getEnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getEnvFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
