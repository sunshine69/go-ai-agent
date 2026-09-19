// Package ragmanager — per-user RAG store manager.
//
// Like MCPManager, ragmanager holds a shared default RAG store and a map of
// per-user overrides keyed by user id.  When a user sets a custom RAG DB path
// via /ragdir the manager lazily opens (or re-opens) that store the next time
// the user sends a message.
package ragmanager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/embeddings"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/ragstore"
)

// ---------------------------------------------------------------------------
// Utility: workdir validation (copied from mcpclient to avoid import cycle)
// ---------------------------------------------------------------------------

// isValidWorkDir reports whether p is a relative path without ".." components.
func isValidWorkDir(p string) bool {
	p = filepath.Clean(p)
	if p == "." {
		return false
	}
	return !strings.Contains(p, "..")
}

// ---------------------------------------------------------------------------
// Manager — per-user RAG-store registry
// ---------------------------------------------------------------------------

// Manager resolves which *ragstore.RAGStore serves a given user and lazily
// opens stores for users with a custom ragDir setting.
type Manager struct {
	defaultRAG *ragstore.RAGStore
	custom     map[string]*ragstore.RAGStore // key = resolved absolute DB path
	dirs       map[string]string             // key = resolved absolute DB path → raw relative dir value
	uidDirs    map[int64]string              // key = user id → raw relative dir value
	cfg        embeddings.Config             // shared across all stores
	dim        int                           // embedding dimension, shared across all stores
	mu         sync.RWMutex
	// openStoreFn lets tests override the store-opening logic so the real
	// Client(), Store(), SetUIDDir() and ResetDirectory() paths can be
	// exercised with a mock embedder (no CGO/sqlite needed). When nil the real
	// openStore is used.
	openStoreFn func(dbPath string) (*ragstore.RAGStore, error)
}

// NewManager builds a manager seeded with the shared default store.
// A nil default simply means "RAG is disabled everywhere" unless overridden.
func NewManager(defaultRAG *ragstore.RAGStore, embedCfg embeddings.Config, dim int) *Manager {
	return &Manager{
		defaultRAG: defaultRAG,
		custom:     make(map[string]*ragstore.RAGStore),
		dirs:       make(map[string]string),
		uidDirs:    make(map[int64]string),
		cfg:        embedCfg,
		dim:        dim,
	}
}

// Client returns the RAG store for the given user id. It resolves the raw
// directory setting for the user (default empty) and delegates to Store().
func (m *Manager) Client(uid int64) *ragstore.RAGStore {
	fmt.Printf("[DEBUG RAGDIR] Client(uid=%d): START\n", uid)
	m.mu.RLock()
	rawDir, ok := m.uidDirs[uid]
	m.mu.RUnlock()
	fmt.Printf("[DEBUG RAGDIR] Client(uid=%d): uidDirs[uid] => rawDir=%q ok=%v\n", uid, rawDir, ok)
	if !ok {
		rawDir = ""
		fmt.Printf("[DEBUG RAGDIR] Client(uid=%d): no uid mapping, using default rawDir=\"\"\n", uid)
	}
	fmt.Printf("[DEBUG RAGDIR] Client(uid=%d): delegating to Store(%q)\n", uid, rawDir)
	store := m.Store(rawDir)
	fmt.Printf("[DEBUG RAGDIR] Client(uid=%d): Store(%q) => store=%p\n", uid, rawDir, store)
	return store
}

// Store returns the RAG store for the given raw relative dir value.
// If the value is empty the default store is returned. Otherwise the manager
// looks up (or creates) a per-user store keyed by its resolved absolute path.
func (m *Manager) Store(rawDir string) *ragstore.RAGStore {
	fmt.Printf("[DEBUG RAGDIR] Store(rawDir=%q): START\n", rawDir)
	if rawDir == "" {
		fmt.Printf("[DEBUG RAGDIR] Store(rawDir=\"\"): returning default store %p\n", m.defaultRAG)
		return m.defaultRAG
	}
	m.mu.RLock()
	resolved, ok := m.dirs[rawDir]
	m.mu.RUnlock()
	fmt.Printf("[DEBUG RAGDIR] Store(rawDir=%q): dirs lookup => resolved=%q ok=%v\n", rawDir, resolved, ok)
	if ok {
		m.mu.RLock()
		s := m.custom[resolved]
		m.mu.RUnlock()
		if s != nil {
			fmt.Printf("[DEBUG RAGDIR] Store(rawDir=%q): cache hit resolved=%q returning store %p\n", rawDir, resolved, s)
			return s
		}
	}
	// Need to create it — lock and do it.
	m.mu.Lock()
	defer m.mu.Unlock()
	if rawDir != "" {
		resolved = filepath.Join(filepath.Clean(rawDir), "rags.db")
		fmt.Printf("[DEBUG RAGDIR] Store(rawDir=%q): resolved path=%q\n", rawDir, resolved)
	}
	if resolved != "" {
		s := m.custom[resolved]
		if s != nil {
			fmt.Printf("[DEBUG RAGDIR] Store(rawDir=%q): custom[resolved=%q] found, returning store %p\n", rawDir, resolved, s)
			return s
		}
	}
	fmt.Printf("[DEBUG RAGDIR] Store(rawDir=%q): calling openStore(resolved=%q)\n", rawDir, resolved)
	store, err := m.openStore(resolved, "")
	if err != nil {
		fmt.Printf("[DEBUG RAGDIR] Store(rawDir=%q): openStore FAILED: %v\n", rawDir, err)
		// Fail loudly instead of silently falling back to the default
		// store. A silent fallback would make a bad /ragdir search run against
		// the wrong (default) data, looking like it succeeded. Returning nil
		// makes ContextBuilder emit a clear "RAG store unavailable" error to the
		// user, so a bad directory is never mistaken for a working one.
		return nil
	}
	fmt.Printf("[DEBUG RAGDIR] Store(rawDir=%q): openStore OK, store=%p\n", rawDir, store)
	m.custom[resolved] = store
	m.dirs[rawDir] = resolved
	return store
}

// ResetDirectory invalidates a previously-opened per-user store so the next
// query re-opens it. This is useful after the user has dropped a new DB at
// the directory (e.g. via the CLI) so the store reflects the latest content.
func (m *Manager) ResetDirectory(rawDir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rawDir == "" {
		return
	}
	resolved, ok := m.dirs[rawDir]
	if !ok {
		return
	}
	if prev := m.custom[resolved]; prev != nil {
		prev.Close()
	}
	delete(m.custom, resolved)
	delete(m.dirs, rawDir)
	// Also remove any uid→rawDir entries that pointed to this rawDir.
	for uid, rd := range m.uidDirs {
		if rd == rawDir {
			delete(m.uidDirs, uid)
		}
	}
}

// ResetByResolvedPath invalidates a per-user store by resolved absolute path.
func (m *Manager) ResetByResolvedPath(resolved string) {
	fmt.Printf("[DEBUG RAGDIR] ResetByResolvedPath(resolved=%q): START\n", resolved)
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev := m.custom[resolved]; prev != nil {
		fmt.Printf("[DEBUG RAGDIR] ResetByResolvedPath(resolved=%q): closing previously-opened store %p\n", resolved, prev)
		prev.Close()
	} else {
		fmt.Printf("[DEBUG RAGDIR] ResetByResolvedPath(resolved=%q): no previously-opened store found\n", resolved)
	}
	delete(m.custom, resolved)
	// Also remove from dirs mapping (may or may not exist)
	delete(m.dirs, resolved)
	fmt.Printf("[DEBUG RAGDIR] ResetByResolvedPath(resolved=%q): DONE\n", resolved)
}

// SetUIDDir records the mapping from user id to their raw RAG directory value.
// An empty value clears the mapping (falls back to the default).
func (m *Manager) SetUIDDir(uid int64, rawDir string) {
	fmt.Printf("[DEBUG RAGDIR] SetUIDDir(uid=%d, rawDir=%q): START\n", uid, rawDir)
	m.mu.Lock()
	defer m.mu.Unlock()
	if rawDir == "" {
		fmt.Printf("[DEBUG RAGDIR] SetUIDDir(uid=%d, rawDir=%q): empty rawDir, deleting uid mapping\n", uid, rawDir)
		// Remove any previously-set mapping so the user falls back to default.
		delete(m.uidDirs, uid)
		return
	}
	m.uidDirs[uid] = rawDir
	fmt.Printf("[DEBUG RAGDIR] SetUIDDir(uid=%d, rawDir=%q): set uidDirs[%d]=%q\n", uid, rawDir, uid, rawDir)
}

// LoadFromDB rehydrates the in-memory uid→rawDir mapping from the persisted
// user_settings table. The mapping lives only in memory in Manager, so a fresh
// backend process (or a reload after /ragdir) would otherwise forget the user's
// choice and silently fall back to the default RAG store. We reload every user's
// stored ragDBPath so the correct store is restored on startup. Invalid values
// are skipped rather than mapped.
func (m *Manager) LoadFromDB(d *db.DB) {
	if d == nil || d.Settings == nil {
		return
	}
	var uids []int64
	if users, err := d.Users.UserViews(); err == nil {
		for _, u := range users {
			uids = append(uids, u.ID)
		}
	}
	if len(uids) == 0 {
		uids = append(uids, 0)
	}
	for _, uid := range uids {
		raw, err := d.Settings.Get(uid, "ragDBPath")
		fmt.Printf("[DEBUG RAGDIR] LoadFromDB(uid=%d): Settings.Get => raw=%q err=%v\n", uid, raw, err)
		if err != nil || strings.TrimSpace(raw) == "" {
			continue
		}
		if !IsValidRagDir(raw) {
			continue
		}
		m.uidDirs[uid] = strings.TrimSpace(raw)
		fmt.Printf("[DEBUG RAGDIR] LoadFromDB(uid=%d): mapped uidDirs[%d]=%q\n", uid, uid, raw)
	}
}

func (m *Manager) openStore(dbPath string, _ string) (*ragstore.RAGStore, error) {
	fmt.Printf("[DEBUG RAGDIR] openStore(dbPath=%q): START (openStoreFn set=%v)\n", dbPath, m.openStoreFn != nil)
	if m.openStoreFn != nil {
		store, err := m.openStoreFn(dbPath)
		fmt.Printf("[DEBUG RAGDIR] openStore(dbPath=%q): hook returned store=%p err=%v\n", dbPath, store, err)
		return store, err
	}
	if dbPath == "" {
		// If no path is given, fall back to the default.
		fmt.Printf("[DEBUG RAGDIR] openStore(dbPath=\"\"): fall back to default store %p\n", m.defaultRAG)
		return m.defaultRAG, nil
	}
	// Ensure the parent directory exists, creating it if necessary
	// (matching the reference CLI's RAG_DB_PATH knob, which lazily
	// creates its directory). A genuine mkdir failure is still an error.
	dir := filepath.Dir(dbPath)
	fmt.Printf("[DEBUG RAGDIR] openStore(dbPath=%q): ensuring parent dir %q exists (MkdirAll)\n", dbPath, dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %q: %w", dir, err)
	}
	fmt.Printf("[DEBUG RAGDIR] openStore(dbPath=%q): parent dir %q created/exists\n", dbPath, dir)
	ragCfg := ragstore.Config{
		Enabled:        true,
		DBPath:         dbPath,
		ChunkSize:      512,
		ChunkOverlap:   50,
		SearchLimit:    5,
		ScoreThreshold: 0.25,
	}
	return ragstore.New(ragCfg, embeddings.New(m.cfg, m.dim))
}

// IsValidRagDir reports whether dir is a valid relative path (no "..", not ".").
func IsValidRagDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	return isValidWorkDir(dir)
}

// DefaultRAGDBPath returns the absolute path of the default RAG DB file so the
// backend can compute a relative value to store in user_settings. The value is
// always relative to the process working directory, matching the Python CLI
// default.
func DefaultRAGDBPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, "rags.db"), nil
}

// RawRAGDir returns the relative directory value stored in user_settings for a
// custom RAG path. If the user's stored value is the same as the default it is
// considered unset (empty). Otherwise the raw relative value is returned.
//
// The caller is responsible for calling resolveRagDBPath() to get the absolute
// path.
func RawRAGDir(dbPath, rawStored string) string {
	trimmed := strings.TrimSpace(rawStored)
	if trimmed == "" {
		return ""
	}
	// Normalise the stored value: resolve it to a candidate absolute path.
	dir := filepath.Join(filepath.Clean(trimmed), "rags.db")
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	// Compare against the candidate default.
	if abs == dbPath {
		return ""
	}
	return trimmed
}

// ResolveRAGDBPath resolves a raw per-user relative dir value into an absolute
// RAG DB file path, mirroring the Python convention of appending "rags.db".
func ResolveRAGDBPath(rawDir string) (string, error) {
	if rawDir == "" {
		return "", nil
	}
	dir := filepath.Join(filepath.Clean(rawDir), "rags.db")
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return abs, nil
}
