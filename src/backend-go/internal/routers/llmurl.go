// Package routers — llmurl.go: per-user LLM base-URL endpoint
// (GET/POST /api/llmurl), backed by the user_settings table under the key
// "llmURL".
//
// The /url slash command lets a user point the *next* chat message at a
// different OpenAI-compatible chat server (for example a local llama.cpp
// server running at http://127.0.0.1:8080). The value is stored per-user and,
// on the backend, is substituted for the configured LLM_BASE_URL when that user
// calls POST /api/messages/stream — so a later message actually streams from
// the chosen server instead of the code/default host. Omit the argument to
// report the current value; a bare /url reset clears it back to the default.
//
// The stored value is the *base* host only (e.g. "http://127.0.0.1:11434" or
// "http://127.0.0.1:8080/v1"); the system appends "/chat/completions" itself.
// A value that merely repeats the default is likewise cleared. Every /url
// request is guarded by requireAuth at the mux, so it only runs for a valid
// bearer token.
//
// The stored shape mirrors the existing per-user settings (see mcpdir.go and
// settings.go): the value is always text and callers decode it as needed.
package routers

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
)

// settingKeyLLMURL is the user_settings key under which the per-user LLM
// base-URL override is stored.
const settingKeyLLMURL = "llmURL"

// defaultLLMBaseURL is the project's configured default LLM host used when a
// user has not set (and has not reset away from) a /url override. It must stay
// in sync with the config default (config.Load() -> envKey("LLM_BASE_URL", ...));
// a drifted constant would silently serve a different host than configured.
// Callers should read it from Config.LLMBASEURL rather than referencing this
// directly, but it exists as the documented contract and for the reset path.
const defaultLLMBaseURL = "http://localhost:11434"

// llmURLResponse is the client-facing view of the per-user LLM base-URL
// selector. It is returned by both GET and POST with the (post-write) value.
type llmURLResponse struct {
	// Value is the raw, base-URL override the user has set. Empty when unset
	// or reset, meaning the backend uses its configured default host.
	Value string `json:"value"`
}

// llmURLHandler serves the per-user LLM base-URL endpoints.
type llmURLHandler struct {
	db *db.DB
}

func newLLMURLHandler(dbStore *db.DB) *llmURLHandler {
	return &llmURLHandler{db: dbStore}
}

// resolveLLMURL returns the caller's effective LLM base-URL override: the
// stored "llmURL" setting (normalized) when set and valid, otherwise "" (unset),
// meaning the backend uses its configured default. It is the single source of
// truth the streaming handlers consult so a user's /url choice shapes the server
// a later message is streamed from.
func resolveLLMURL(dbStore *db.DB, uid int64) string {
	if dbStore == nil || dbStore.Settings == nil {
		return ""
	}
	raw, err := dbStore.Settings.Get(uid, settingKeyLLMURL)
	if err != nil {
		return ""
	}
	return normalizeLLMBaseURL(raw)
}

// normalizeLLMBaseURL cleans a raw stored value into the canonical base form:
// it trims surrounding whitespace and drops a trailing "/chat/completions" so
// the value stays a base host (the system appends that path itself). An empty
// result is returned as "".
func normalizeLLMBaseURL(raw string) string {
	v := strings.TrimSpace(raw)
	v = strings.TrimRight(v, "/") // drop trailing slash first ...
	v = strings.TrimSuffix(v, "/chat/completions") // ... then the path
	if strings.EqualFold(v, defaultLLMBaseURL) {
		// A value identical to the default carries no information; return
		// empty so it is treated as unset/reset and falls back to config.
		return ""
	}
	return v
}

// isValidLLMBaseURL reports whether s is a usable OpenAI-compatible chat base
// URL: an http(s) scheme with a non-empty host (optionally carrying a base path
// such as "/v1"). It deliberately rejects the full "/chat/completions" endpoint
// and a bare scheme-less host so the stored value stays a base the backend can
// safely append to.
func isValidLLMBaseURL(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	u, err := url.Parse(strings.ToLower(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Host
	if host == "" {
		return false
	}
	// Reject values that already end in the chat-completions path; those are
	// normalized away on storage and rejected here so the user corrects them.
	if strings.HasSuffix(host, "/chat/completions") || strings.HasSuffix(s, "/chat/completions") {
		return false
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		// No port present — a bare host is not a usable OpenAI base (the
		// backend appends /chat/completions to host only). Accepting it would
		// silently target the wrong URL. Reject so the user includes :port.
		return false
	}
	return true
}

// handleList GET /api/llmurl — return the caller's stored LLM base-URL override.
func (h *llmURLHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, llmURLResponse{Value: resolveLLMURL(h.db, uid)})
}

// handleSet POST /api/llmurl — set (or reset) the caller's LLM base-URL. Body:
// {"value": "http://127.0.0.1:8080"}. An empty value or a value equal to the
// default clears the stored value so the backend falls back to its configured
// default host. Any other value must be a valid OpenAI-compatible base URL.
func (h *llmURLHandler) handleSet(w http.ResponseWriter, r *http.Request) {
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
	trimmed := strings.TrimSpace(req.Value)

	// An empty value or one identical to the default clears the setting
	// (falls back to the backend configured default): drop the stored value.
	if trimmed == "" || normalizeLLMBaseURL(trimmed) == "" {
		if h.db != nil && h.db.Settings != nil {
			if err := h.db.Settings.Delete(uid, settingKeyLLMURL); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to reset LLM URL")
				return
			}
		}
		writeJSON(w, http.StatusOK, llmURLResponse{Value: ""})
		return
	}
	// Validate the stored base up front so only usable overrides persist.
	if !isValidLLMBaseURL(trimmed) {
		writeError(w, http.StatusBadRequest,
			"LLM URL must be an http(s) base with a host and port, e.g. http://127.0.0.1:11434")
		return
	}
	if err := h.db.Settings.Set(uid, settingKeyLLMURL, trimmed); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save LLM URL")
		return
	}
	writeJSON(w, http.StatusOK, llmURLResponse{Value: resolveLLMURL(h.db, uid)})
}
