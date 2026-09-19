package ragmanager

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/embeddings"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/ragstore"
)

// --- test doubles -----------------------------------------------------------

type mockEmbedder struct {
	dim int
}

func (m *mockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, m.dim)
		for j := range out[i] {
			out[i][j] = float32(i*100 + j)
		}
	}
	return out, nil
}

func (m *mockEmbedder) Dimensions() int { return m.dim }

func cfgFor(path string) ragstore.Config {
	return ragstore.Config{
		Enabled:        true,
		DBPath:         path,
		ChunkSize:      512,
		ChunkOverlap:   50,
		SearchLimit:    5,
		ScoreThreshold: 0.25,
	}
}

// storeRecorder is an openStoreFn implementation that creates real RAGStores
// backed by a mock embedder and records the resolved db paths it was asked to
// open. It lets the real Client()/Store()/SetUIDDir()/ResetDirectory() methods
// run end-to-end without CGO in the manager package tests.
type storeRecorder struct {
	mu       sync.Mutex
	dim      int
	embedder *mockEmbedder
	log      []string
}

func newStoreRecorder(dim int) *storeRecorder {
	return &storeRecorder{dim: dim, embedder: &mockEmbedder{dim: dim}}
}

func (r *storeRecorder) openFn(resolved string) (*ragstore.RAGStore, error) {
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return nil, err
	}
	s, err := ragstore.New(cfgFor(resolved), r.embedder)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.log = append(r.log, resolved)
	r.mu.Unlock()
	return s, nil
}

// --- tests -------------------------------------------------------------------

func TestClientRouting(t *testing.T) {
	dim := 4
	defaultDB := filepath.Join(t.TempDir(), "default.rags.db")
	defaultStore, err := ragstore.New(cfgFor(defaultDB), &mockEmbedder{dim: dim})
	if err != nil {
		t.Fatalf("new default store: %v", err)
	}
	defer defaultStore.Close()
	defer os.RemoveAll("custom")

	rec := newStoreRecorder(dim)
	mgr := NewManager(defaultStore, embeddings.Config{}, dim)
	mgr.openStoreFn = rec.openFn

	// User 0 (no ragDir) -> default store.
	if got := mgr.Client(0); got != defaultStore {
		t.Fatalf("Client(0) = %v, want default store %v", got, defaultStore)
	}

	// Per-user custom dir.
	mgr.SetUIDDir(42, "custom")
	custom := mgr.Client(42)
	if custom == nil {
		t.Fatal("Client(42) = nil, want a store")
	}
	if custom == defaultStore {
		t.Error("Client(42) returned the default store, want a custom one")
	}
	if want := filepath.Join("custom", "rags.db"); len(rec.log) != 1 || rec.log[0] != want {
		t.Errorf("opened log = %v, want [%s]", rec.log, want)
	}

	// Stable identity.
	if got := mgr.Client(42); got != custom {
		t.Error("Client(42) identity changed on second call")
	}

	// A different user with no dir -> default.
	if got := mgr.Client(7); got != defaultStore {
		t.Error("Client(7) should be default store")
	}

	// Clear the user's dir -> back to default.
	mgr.SetUIDDir(42, "")
	if got := mgr.Client(42); got != defaultStore {
		t.Error("after SetUIDDir(42, ''), Client(42) should be default")
	}
}

func TestResetDirectory(t *testing.T) {
	dim := 4
	defaultDB := filepath.Join(t.TempDir(), "default.rags.db")
	defaultStore, err := ragstore.New(cfgFor(defaultDB), &mockEmbedder{dim: dim})
	if err != nil {
		t.Fatalf("new default store: %v", err)
	}
	defer defaultStore.Close()
	defer os.RemoveAll("custom")

	rec := newStoreRecorder(dim)
	mgr := NewManager(defaultStore, embeddings.Config{}, dim)
	mgr.openStoreFn = rec.openFn

	// Set a custom dir for user 1 and open the store once.
	mgr.SetUIDDir(1, "custom")
	first := mgr.Client(1)
	if first == nil || first == defaultStore {
		t.Fatal("expected custom store for user 1")
	}
	if len(rec.log) != 1 {
		t.Fatalf("expected 1 open before reset, got %v", rec.log)
	}

	// ResetDirectory invalidates the open store. It also drops the uid->rawDir
	// mapping, so the next Client() call falls back to default.
	mgr.ResetDirectory("custom")
	if got := mgr.Client(1); got != defaultStore {
		t.Error("after ResetDirectory, Client(1) should fall back to default")
	}

	// Re-registering the dir reopens a fresh (new) instance, matching the
	// "user dropped a new DB into the directory" scenario.
	mgr.SetUIDDir(1, "custom")
	second := mgr.Client(1)
	if second == nil || second == defaultStore {
		t.Fatal("expected a custom store after re-SetUIDDir")
	}
	if second == first {
		t.Error("ResetDirectory did not lead to a new store instance on reopen")
	}
	if len(rec.log) < 2 {
		t.Errorf("expected at least 2 opens after reopen, got %v", rec.log)
	}
}

func TestDefaultNil(t *testing.T) {
	mgr := NewManager(nil, embeddings.Config{}, 4)
	if got := mgr.Client(0); got != nil {
		t.Error("Client(0) with nil default should be nil")
	}
}

func TestIsValidRagDir(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{".", false},
		{"custom", true},
		{"a/b/c", true},
		{"../x", false},
		{"x/..", false},
		{"  spaced  ", true},
	}
	for _, c := range cases {
		if got := IsValidRagDir(c.in); got != c.want {
			t.Errorf("IsValidRagDir(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// --- focused test for the default-store fallback at openStore line ~222 ------

func TestOpenStoreEmptyPathFallsBackToDefault(t *testing.T) {
	dim := 4
	defaultDB := filepath.Join(t.TempDir(), "default.rags.db")
	defaultStore, err := ragstore.New(cfgFor(defaultDB), &mockEmbedder{dim: dim})
	if err != nil {
		t.Fatalf("new default store: %v", err)
	}
	defer defaultStore.Close()

	// NOTE: openStoreFn is left nil on purpose so the REAL openStore code path
	// (including the "dbPath == \"\" -> return m.defaultRAG" fallback at line 222)
	// is exercised, not the mock hook.
	mgr := NewManager(defaultStore, embeddings.Config{}, dim)

	got, err := mgr.openStore("", "")
	if err != nil {
		t.Fatalf("openStore(\"\") returned an error: %v", err)
	}
	if got != defaultStore {
		t.Errorf("openStore(\"\") = %v, want the shared default store %v", got, defaultStore)
	}
}

func TestOpenStoreNonEmptyPathUsesHook(t *testing.T) {
	dim := 4
	defaultDB := filepath.Join(t.TempDir(), "default.rags.db")
	defaultStore, err := ragstore.New(cfgFor(defaultDB), &mockEmbedder{dim: dim})
	if err != nil {
		t.Fatalf("new default store: %v", err)
	}
	defer defaultStore.Close()

	rec := newStoreRecorder(dim)
	mgr := NewManager(defaultStore, embeddings.Config{}, dim)
	mgr.openStoreFn = rec.openFn // use the mock hook, not the real openStore

	// With the hook installed, a non-empty path is routed through it and never
	// hits the line-222 fallback.
	got, err := mgr.openStore("custom/rags.db", "")
	if err != nil {
		t.Fatalf("openStore(\"custom/rags.db\") error: %v", err)
	}
	if got == nil || got == defaultStore {
		t.Error("openStore with non-empty path should use the hook, not the default store")
	}
	if len(rec.log) != 1 || rec.log[0] != "custom/rags.db" {
		t.Errorf("hook log = %v, want [custom/rags.db]", rec.log)
	}
}

func TestOpenStoreDefaultWhenNoDefault(t *testing.T) {
	// A Manager with a nil default store must hand back nil from the fallback,
	// so a caller can nil-guard and surface "RAG unavailable".
	mgr := NewManager(nil, embeddings.Config{}, 4)
	got, err := mgr.openStore("", "")
	if err != nil {
		t.Fatalf("openStore(\"\") error: %v", err)
	}
	if got != nil {
		t.Errorf("openStore(\"\") = %v, want nil (no default)", got)
	}
}
