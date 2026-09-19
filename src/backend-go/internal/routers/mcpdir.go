// Package routers — mcpdir.go: per-user MCP working-directory endpoint
// (GET/POST /api/mcpdir), backed by the user_settings table under the key
// "mcpWorkdir".
//
// The /mcpdir slash command lets a user pick the directory the default (user-0)
// MCP stdio server runs in, mirroring the reference CLI's MCP_WORKDIR knob. The
// value is a relative path with no ".." component (e.g. "mcp_data"), which the
// backend validates up front and forwards to mcpclient.NewManager at startup —
// the existing resolveConnectWorkdir path re-validates it, resolves it to an
// absolute path, and creates it if missing. Every /mcpdir request is guarded by
// requireAuth at the mux, so it only runs for a valid bearer token.
//
// The stored shape mirrors the existing per-user settings (see settings.go):
// the value is always text and callers decode it as needed.
package routers

import (
	"net/http"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/mcpclient"
)

// mcpdirResponse is the client-facing view of the per-user MCP working
// directory. It is returned by both GET and POST with the (post-write) value.
type mcpdirResponse struct {
	// Value is the raw, relative, ".."-free working-directory selector the
	// user has set (e.g. "mcp_data"). Empty when unset.
	Value string `json:"value"`
}

// mcpdirHandler serves the per-user MCP working-directory endpoints.
type mcpdirHandler struct {
	db *db.DB
}

func newMCPdirHandler(dbStore *db.DB) *mcpdirHandler {
	return &mcpdirHandler{db: dbStore}
}

// resolveDefaultMCPWorkdir returns the caller's effective MCP working-directory
// selector: the stored "mcpWorkdir" setting when valid, otherwise "" (unset).
// It is the single source of truth the backend consults when it seeds the
// default (user-0) MCP server's workdir at startup, so the value chosen via
// /mcpdir actually shapes where the stdio child runs.
func resolveDefaultMCPWorkdir(dbStore *db.DB, uid int64) (string, bool) {
	if dbStore == nil || dbStore.Settings == nil {
		return "", false
	}
	raw, err := dbStore.Settings.Get(uid, "mcpWorkdir")
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	if !mcpclient.IsValidWorkdir(trimmed) {
		return "", false
	}
	return trimmed, true
}

// handleList GET /api/mcpdir — return the caller's stored MCP working directory.
func (h *mcpdirHandler) handleList(w http.ResponseWriter, r *http.Request) {
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
	if v, valid := resolveDefaultMCPWorkdir(h.db, uid); valid {
		value = v
	}
	writeJSON(w, http.StatusOK, mcpdirResponse{Value: value})
}

// handleSet POST /api/mcpdir — set (or clear) the caller's MCP working
// directory. Body: {"value": "mcp_data"} (empty value clears the setting). The
// value is validated server-side before it is stored so only usable selectors
// ever persist.
func (h *mcpdirHandler) handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		Value string `json:"value"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	value := strings.TrimSpace(req.Value)

	// An empty value clears the setting (falls back to the backend cwd); a
	// non-empty value must be a valid working-directory selector or it is
	// rejected.
	if value != "" {
		if !mcpclient.IsValidWorkdir(value) {
			writeError(w, http.StatusBadRequest,
				"workdir must be relative with no '..' component")
			return
		}
	}
	if err := h.db.Settings.Set(uid, "mcpWorkdir", value); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workdir")
		return
	}

	writeJSON(w, http.StatusOK, mcpdirResponse{Value: value})
}
