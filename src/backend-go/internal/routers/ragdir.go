// Package routers — ragdir.go: per-user RAG DB directory endpoint
// (GET/POST /api/ragdir), backed by the user_settings table under the key
// "ragDBPath".
//
// The /ragdir slash command lets a user pick the directory that the default
// RAG SQLite DB lives in, mirroring the reference CLI's RAG_DB_PATH knob.
// The value is a relative path with no ".." component (e.g. "rag_data"),
// which the backend validates up front and resolves to
// <value>/rags.db when the manager opens the store. Every /ragdir
// request is guarded by requireAuth at the mux, so it only runs for a valid
// bearer token.
package routers

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/ragmanager"
)

// ragdirResponse is the client-facing view of the per-user RAG directory.
type ragdirResponse struct {
	// Value is the raw, relative, ".."-free directory selector the user has set
	// (e.g. "rag_data"). Empty when unset / using the default.
	Value string `json:"value"`
}

// ragdirHandler serves the per-user RAG directory endpoints.
type ragdirHandler struct {
	db       *db.DB
	rmanager *ragmanager.Manager
}

func newRagdirHandler(dbStore *db.DB, rmanager *ragmanager.Manager) *ragdirHandler {
	return &ragdirHandler{db: dbStore, rmanager: rmanager}
}

// resolveDefaultRAGDBPath returns the caller's effective RAG DB path: the stored
// "ragDBPath" setting when valid, otherwise the default.
// It returns the raw relative value (empty when unset / default).
func resolveDefaultRAGDBPath(dbStore *db.DB, uid int64) (string, bool) {
	if dbStore == nil || dbStore.Settings == nil {
		fmt.Printf("[DEBUG RAGDIR] resolveDefaultRAGDBPath: dbStore nil, returning (\"\", false)\n")
		return "", false
	}
	raw, err := dbStore.Settings.Get(uid, "ragDBPath")
	fmt.Printf("[DEBUG RAGDIR] resolveDefaultRAGDBPath: Settings.Get(uid=%d, key=\"ragDBPath\") => raw=%q err=%v\n", uid, raw, err)
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	fmt.Printf("[DEBUG RAGDIR] resolveDefaultRAGDBPath: trimmed=%q\n", trimmed)
	if trimmed == "" {
		fmt.Printf("[DEBUG RAGDIR] resolveDefaultRAGDBPath: trimmed empty, returning (\"\", false)\n")
		return "", false
	}
	if !ragmanager.IsValidRagDir(trimmed) {
		fmt.Printf("[DEBUG RAGDIR] resolveDefaultRAGDBPath: %q invalid, returning (\"\", false)\n", trimmed)
		return "", false
	}
	fmt.Printf("[DEBUG RAGDIR] resolveDefaultRAGDBPath: returning (%q, true)\n", trimmed)
	return trimmed, true
}

// handleList GET /api/ragdir — return the caller's stored RAG directory.
func (h *ragdirHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	value := ""
	if v, valid := resolveDefaultRAGDBPath(h.db, uid); valid {
		value = v
	}
	fmt.Printf("[DEBUG RAGDIR] handleList: uid=%d value=%q => writing response\n", uid, value)
	writeJSON(w, http.StatusOK, ragdirResponse{Value: value})
}

// handleSet POST /api/ragdir — set (or clear) the caller's RAG directory.
// Body: {"value": "rag_data"} (empty value clears the setting, falling back to
// the default). The value is validated server-side before it is stored so only
// usable selectors ever persist. After saving, the manager is notified so the
// next request will use the new store.
//
// The directory does NOT have to exist yet: the value is stored as-is and the
// manager lazily creates the directory (and an empty store) on the next search.
// A not-yet-existing path is therefore accepted, not rejected.
func (h *ragdirHandler) handleSet(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("[DEBUG RAGDIR] handleSet: START method=%s\n", r.Method)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	fmt.Printf("[DEBUG RAGDIR] handleSet: uid=%d\n", uid)

	var req struct {
		Value string `json:"value"`
	}
	if err := decodeBody(r, &req); err != nil {
		fmt.Printf("[DEBUG RAGDIR] handleSet: decodeBody error: %v\n", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	value := strings.TrimSpace(req.Value)
	fmt.Printf("[DEBUG RAGDIR] handleSet: raw value from body=%q trimmed=%q\n", req.Value, value)

	// An empty value clears the setting (falls back to the default). A
	// non-empty value must be a valid RAG dir selector; the referenced
	// directory is not required to exist — openStore lazily creates it on
	// the next search.
	if value != "" && !ragmanager.IsValidRagDir(value) {
		fmt.Printf("[DEBUG RAGDIR] handleSet: %q is INVALID rag dir\n", value)
		writeError(w, http.StatusBadRequest,
			"ragdir must be a relative directory with no '..' component")
		return
	}
	fmt.Printf("[DEBUG RAGDIR] handleSet: %q is VALID rag dir\n", value)
	if err := h.db.Settings.Set(uid, "ragDBPath", value); err != nil {
		fmt.Printf("[DEBUG RAGDIR] handleSet: Settings.Set(uid=%d, key=\"ragDBPath\", value=%q) FAILED: %v\n", uid, value, err)
		writeError(w, http.StatusInternalServerError, "failed to save rag dir")
		return
	}
	fmt.Printf("[DEBUG RAGDIR] handleSet: Settings.Set(uid=%d, key=\"ragDBPath\", value=%q) OK\n", uid, value)

	// Invalidate any previously-opened per-user store so the next query
	// reopens the correct DB. Also register the uid → rawDir mapping so
	// ragmanager.Client(uid) can look it up.
	if value != "" {
		resolved, err := ragmanager.ResolveRAGDBPath(value)
		if err != nil {
			// Best-effort: still return success since the setting is saved.
			fmt.Printf("[DEBUG RAGDIR] handleSet: ResolveRAGDBPath(%q) error: %v, best-effort join\n", value, err)
			resolved = filepath.Join(filepath.Clean(value), "rags.db")
		} else {
			fmt.Printf("[DEBUG RAGDIR] handleSet: ResolveRAGDBPath(%q) => resolved=%q\n", value, resolved)
		}
		fmt.Printf("[DEBUG RAGDIR] handleSet: ResetByResolvedPath(%q)\n", resolved)
		h.rmanager.ResetByResolvedPath(resolved)
	}
	fmt.Printf("[DEBUG RAGDIR] handleSet: SetUIDDir(uid=%d, value=%q)\n", uid, value)
	h.rmanager.SetUIDDir(uid, value)

	fmt.Printf("[DEBUG RAGDIR] handleSet: writing success response value=%q\n", value)
	writeJSON(w, http.StatusOK, ragdirResponse{Value: value})
}
