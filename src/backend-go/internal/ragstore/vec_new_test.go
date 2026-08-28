package ragstore

import (
	"context"
	"os"
	"testing"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

type mockEmbedder struct{}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, 4)
		for j := range out[i] {
			out[i][j] = float32(i*10 + j)
		}
	}
	return out, nil
}

func (m *mockEmbedder) Dimensions() int { return 4 }

func TestRAGStoreNewCGO(t *testing.T) {
	sqlite_vec.Auto()
	dir := t.TempDir()
	dbPath := dir + "/rag.db"

	cfg := Config{
		Enabled:        true,
		DBPath:         dbPath,
		ChunkSize:      1500,
		ChunkOverlap:   300,
		SearchLimit:    2,
		ScoreThreshold: 0.25,
	}

	store, err := New(cfg, &mockEmbedder{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	chunks := []Chunk{
		{ChunkID: "doc1#pid#1", Content: "Go is a statically typed, compiled programming language designed for simplicity and efficiency.", SourceFile: "/docs/go.md", SourceCategory: "tech", DocumentType: "markdown", Title: "Go Language", PageNumber: 1},
		{ChunkID: "doc2#pid#1", Content: "SQLite is a C-language library that implements a small, fast, self-contained, high-reliability, full-featured SQL database engine.", SourceFile: "/docs/sqlite.md", SourceCategory: "tech", DocumentType: "markdown", Title: "SQLite Database", PageNumber: 1},
	}

	added, err := store.AddDocuments(ctx, chunks)
	if err != nil {
		t.Fatalf("AddDocuments() failed: %v", err)
	}
	if added != len(chunks) {
		t.Errorf("expected %d chunks added, got %d", len(chunks), added)
	}

	results, err := store.Search(ctx, "programming language", 2, "")
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least one result")
	} else {
		t.Logf("found %d results for 'programming language'", len(results))
	}

	cats, err := store.GetCategories(ctx)
	if err != nil {
		t.Fatalf("GetCategories() failed: %v", err)
	}
	hasTech := false
	for _, c := range cats {
		if c == "tech" {
			hasTech = true
			break
		}
	}
	if !hasTech {
		t.Error("expected 'tech' in categories")
	}

	stats, err := store.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats() failed: %v", err)
	}
	if stats.TotalChunks != len(chunks) {
		t.Errorf("expected %d total chunks, got %d", len(chunks), stats.TotalChunks)
	}
}

func TestRAGStoreNewCGO_Categories(t *testing.T) {
	sqlite_vec.Auto()
	dir := t.TempDir()
	dbPath := dir + "/rag_cat.db"

	cfg := Config{
		Enabled:        true,
		DBPath:         dbPath,
		ChunkSize:      1500,
		ChunkOverlap:   300,
		SearchLimit:    2,
		ScoreThreshold: 0.25,
	}

	store, err := New(cfg, &mockEmbedder{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	chunks := []Chunk{
		{ChunkID: "doc1#pid#1", Content: "Go is a statically typed, compiled programming language designed for simplicity and efficiency.", SourceFile: "/docs/go.md", SourceCategory: "tech", DocumentType: "markdown", Title: "Go Language", PageNumber: 1},
		{ChunkID: "doc2#pid#1", Content: "SQLite is a C-language library that implements a small, fast, self-contained, high-reliability, full-featured SQL database engine.", SourceFile: "/docs/sqlite.md", SourceCategory: "tech", DocumentType: "markdown", Title: "SQLite Database", PageNumber: 1},
		{ChunkID: "doc3#pid#1", Content: "Python is an interpreted high-level general-purpose programming language.", SourceFile: "/docs/python.md", SourceCategory: "dev", DocumentType: "markdown", Title: "Python Language", PageNumber: 1},
	}

	if _, err := store.AddDocuments(ctx, chunks); err != nil {
		t.Fatalf("AddDocuments() failed: %v", err)
	}

	resultsTech, err := store.Search(ctx, "database engine", 2, "tech")
	if err != nil {
		t.Fatalf("Search(tech) failed: %v", err)
	}
	for _, r := range resultsTech {
		if r.SourceCategory != "tech" {
			t.Errorf("expected 'tech' category, got '%s'", r.SourceCategory)
		}
	}

	resultsDev, err := store.Search(ctx, "programming language", 2, "dev")
	if err != nil {
		t.Fatalf("Search(dev) failed: %v", err)
	}
	for _, r := range resultsDev {
		if r.SourceCategory != "dev" {
			t.Errorf("expected 'dev' category, got '%s'", r.SourceCategory)
		}
	}

	resultsAll, err := store.Search(ctx, "language", 5, "")
	if err != nil {
		t.Fatalf("Search(all) failed: %v", err)
	}
	if len(resultsAll) < 2 {
		t.Errorf("expected at least two results for 'language', got %d", len(resultsAll))
	}

	cats, err := store.GetCategories(ctx)
	if err != nil {
		t.Fatalf("GetCategories() failed: %v", err)
	}
	expectedCats := map[string]bool{"tech": true, "dev": true}
	for _, c := range cats {
		delete(expectedCats, c)
	}
	if len(expectedCats) > 0 {
		t.Errorf("expected categories tech and dev, missing: %v", expectedCats)
	}

	stats, err := store.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats() failed: %v", err)
	}
	if stats.TotalChunks != len(chunks) || stats.Dimensions != 4 {
		t.Errorf("expected TotalChunks=%d Dimensions=4, got %+v", len(chunks), stats)
	}

	if err := store.DeleteCollection(ctx); err != nil {
		t.Fatalf("DeleteCollection() failed: %v", err)
	}
	stats2, _ := store.GetStats(ctx)
	if !stats2.Disabled && stats2.TotalChunks != 0 {
		t.Errorf("expected zero chunks after DeleteCollection, got %+v", stats2)
	}
}

func TestRAGStoreNewCGO_Disabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/rag_disabled.db"

	cfg := Config{
		Enabled:        false,
		DBPath:         dbPath,
		ChunkSize:      1500,
		ChunkOverlap:   300,
		SearchLimit:    2,
		ScoreThreshold: 0.25,
	}

	store, err := New(cfg, &mockEmbedder{})
	if err != nil {
		t.Fatalf("New(disabled) failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	chunks := []Chunk{
		{ChunkID: "doc1#pid#1", Content: "This should not be indexed.", SourceFile: "/docs/ignored.md", SourceCategory: "ignore", DocumentType: "markdown", Title: "Ignored", PageNumber: 1},
	}

	added, err := store.AddDocuments(ctx, chunks)
	if err != nil {
		t.Fatalf("AddDocuments(disabled) failed: %v", err)
	}
	if added != 0 {
		t.Errorf("disabled store should not add chunks, got %d", added)
	}

	results, err := store.Search(ctx, "anything", 2, "")
	if err != nil {
		t.Fatalf("Search(disabled) failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("disabled store should return no results, got %d", len(results))
	}

	stats, err := store.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats(disabled) failed: %v", err)
	}
	if !stats.Disabled || stats.TotalChunks != 0 {
		t.Errorf("expected disabled=true and TotalChunks=0, got %+v", stats)
	}
}

func TestRAGStoreNewCGO_EmbeddingSerialization(t *testing.T) {
	tests := []struct {
		name     string
		input    []float32
		wantSize int
	}{
		{"empty vector", []float32{}, 0},
		{"single float32", []float32{1.5}, 4},
		{"four floats (4D)", []float32{1, 2, 3, 4}, 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := serializeFloat32(tt.input)
			if len(out) != tt.wantSize {
				t.Errorf("serializeFloat32(%v): got %d bytes, want %d", tt.input, len(out), tt.wantSize)
			}
		})
	}

	bigVec := make([]float32, 384) // MiniLM-L6-v2 dimension
	for i := range bigVec {
		bigVec[i] = float32(i) / 100.0
	}
	out := serializeFloat32(bigVec)
	if len(out) != 384*4 {
		t.Errorf("serialize Float32(384D): got %d bytes, want %d", len(out), 384*4)
	}
}

func TestConfigFromEnv(t *testing.T) {
	os.Setenv("RAG_ENABLED", "false")
	defer os.Unsetenv("RAG_ENABLED")

	cfg := ConfigFromEnv()
	if cfg.Enabled {
		t.Error("expected RAG disabled from env var")
	}

	os.Setenv("RAG_SQLITE_PATH", "/custom/rag.db")
	os.Setenv("RAG_CHUNK_SIZE", "1024")
	defer os.Unsetenv("RAG_SQLITE_PATH")
	defer os.Unsetenv("RAG_CHUNK_SIZE")

	cfg = ConfigFromEnv()
	if cfg.DBPath != "/custom/rag.db" {
		t.Errorf("expected DBPath='/custom/rag.db', got '%s'", cfg.DBPath)
	}
	if cfg.ChunkSize != 1024 {
		t.Errorf("expected ChunkSize=1024, got %d", cfg.ChunkSize)
	}
}
