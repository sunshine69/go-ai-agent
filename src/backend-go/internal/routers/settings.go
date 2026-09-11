// Package routers — settings.go: per-user application settings endpoint
// (/api/settings), backed by the user_settings table.
//
// The /ctx command (and any future command-mode setting) is persisted per user
// here rather than as a process-wide environment knob. The value of every
// setting is always stored as text; the SPA (and callers) decode it as needed.
// The single setting defined so far is:
//
//	ctxLimit — a positive integer: the estimated token budget of a
//	conversation above which its history is compressed before the request is
//	sent to the LLM. When unset, the backend falls back to a default (see
//	defaultCtxLimit) of 50000.
//
// All requests must be authenticated (guarded by requireAuth at the mux).
package routers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/stevek/go-ai-agent/backend-go/internal/db"
)

// defaultCtxLimit is the effective context token default when a user has never
// set /ctx. Per the project decision: nobody set /ctx → start with 50000.
const defaultCtxLimit = 50000

// settingsResponse is the client-facing view of a user's settings.
type settingsResponse struct {
	// ContextLimit is the user's configured context token budget. When the
	// user has not set one it reflects the default.
	ContextLimit int `json:"context_limit"`
	// Raw is the full set of stored key/value settings (value always a string).
	Raw map[string]string `json:"raw"`
}

// settingsHandler serves the per-user settings endpoints.
type settingsHandler struct {
	db *db.DB
}

func newSettingsHandler(dbStore *db.DB) *settingsHandler {
	return &settingsHandler{db: dbStore}
}

// parseCtxLimit decodes ctxLimit from a raw setting value. It returns the value
// and true only when the value is a positive integer; otherwise it returns 0 and
// false, signaling the caller to fall back to the default.
func parseCtxLimit(value string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// resolveCtxLimit returns the caller's effective context token budget: the
// stored ctxLimit setting when valid, otherwise the default. It is the single
// source of truth the message handlers consult so a user's /ctx choice actually
// shapes context compression.
func resolveCtxLimit(dbStore *db.DB, uid int64) (int, bool) {
	v, err := dbStore.Settings.Get(uid, "ctxLimit")
	if err != nil {
		return 0, false
	}
	return parseCtxLimit(v)
}

// handleList GET /api/settings — return the caller's settings (decoded).
func (h *settingsHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	raw := map[string]string{}
	settings, err := h.db.Settings.GetAll(uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read settings")
		return
	}
	for _, s := range settings {
		raw[s.Key] = s.Value
	}

	ctxLimit := defaultCtxLimit
	if n, ok := resolveCtxLimit(h.db, uid); ok {
		ctxLimit = n
	}

	writeJSON(w, http.StatusOK, settingsResponse{Raw: raw, ContextLimit: ctxLimit})
}

// handleSet POST /api/settings — set a single setting for the caller.
// Body: {"key": "ctxLimit", "value": "32000"}
func (h *settingsHandler) handleSet(w http.ResponseWriter, r *http.Request) {
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
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	key := strings.TrimSpace(req.Key)
	value := strings.TrimSpace(req.Value)
	if key == "" || value == "" {
		writeError(w, http.StatusBadRequest, "key and value are required")
		return
	}
	// Only allow keys this endpoint manages.
	if key != "ctxLimit" {
		writeError(w, http.StatusBadRequest, "unknown setting key")
		return
	}
	// ctxLimit must be a positive integer (matches the value the backend
	// consumes); validate the stored text up front.
	if _, ok := parseCtxLimit(value); !ok {
		writeError(w, http.StatusBadRequest, "ctxLimit must be a positive integer")
		return
	}

	if err := h.db.Settings.Set(uid, key, value); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save setting")
		return
	}

	// Decode the just-saved value for the response; on the happy path parseCtxLimit
	// returned true during validation so this will also be true.
	ctxLimit := defaultCtxLimit
	if n, ok := resolveCtxLimit(h.db, uid); ok {
		ctxLimit = n
	}

	writeJSON(w, http.StatusOK, settingsResponse{
		Raw:          map[string]string{key: value},
		ContextLimit: ctxLimit,
	})
}

