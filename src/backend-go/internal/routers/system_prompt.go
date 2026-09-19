// Package routers — system_prompt.go: per-user custom system prompt endpoint
// (GET/POST /api/system), backed by the user_settings table under the key
// "systemPrompt".
//
// The /sys slash command lets a user override the prompt the LLM is seeded
// with. The /sys <message> variant stores the user's text; /sys default clears
// the stored value so the backend falls back to the code default; running /sys
// with no argument prints the effective (resolved) prompt.
//
// Every message path (hybrid, streaming, tool-use) resolves the prompt from
// this setting at request time and always places it as the FIRST message in the
// conversation, so a user's /sys choice actually shapes how the model behaves
// across all of that user's sessions and chats.
package routers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
)

// settingKeySystemPrompt is the user_settings key under which the per-user
// custom system prompt is stored.
const settingKeySystemPrompt = "systemPrompt"

// systemPromptResponse is the client-facing view of the resolved system prompt,
// returned by both GET and POST with the (post-write) value.
type systemPromptResponse struct {
	// SystemPrompt is the raw, effective system prompt the backend will seed
	// as the first message on the next request. It is the stored custom value
	// when set, otherwise the code default (see defaultSystemPrompt).
	SystemPrompt string `json:"system_prompt"`
}

// systemPromptHandler serves the per-user system-prompt endpoints.
type systemPromptHandler struct {
	db *db.DB
}

func newSystemPromptHandler(dbStore *db.DB) *systemPromptHandler {
	return &systemPromptHandler{db: dbStore}
}

// resolveSystemPrompt returns the effective system prompt for the given
// authenticated caller: the stored custom prompt (verbatim) when set, otherwise
// the code default (see defaultSystemPrompt). It is the single source of truth
// every message path consults so a user's /sys choice shapes the prompt sent to
// the LLM. The result is always placed as the FIRST message in the conversation.
func resolveSystemPrompt(dbStore *db.DB, uid int64) string {
	if dbStore != nil && dbStore.Settings != nil {
		raw, err := dbStore.Settings.Get(uid, settingKeySystemPrompt)
		if err == nil {
			trimmed := strings.TrimSpace(raw)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return defaultSystemPrompt()
}

// handleList GET /api/system — return the caller's effective system prompt
// (stored custom value or the code default).
func (h *systemPromptHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	prompt := resolveSystemPrompt(h.db, uid)
	writeJSON(w, http.StatusOK, systemPromptResponse{SystemPrompt: prompt})
}

// handleSet POST /api/system — set (or reset) the caller's custom system
// prompt. Body: {"prompt": "..."}. A prompt equal to the literal token "default"
// (case-insensitive) clears the stored value, falling back to the code default.
// Any other non-empty text is stored verbatim; an empty payload also clears the
// setting (equivalent to the "default" token).
func (h *systemPromptHandler) handleSet(w http.ResponseWriter, r *http.Request) {
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
		Prompt string `json:"prompt"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)

	// The "default" token (or an empty payload) resets to the code default:
	// drop the stored value so resolveSystemPrompt falls back to the default.
	if prompt == "" || strings.EqualFold(prompt, "default") {
		if h.db != nil && h.db.Settings != nil {
			err := h.db.Settings.Delete(uid, settingKeySystemPrompt)
			// A missing key is a benign "nothing to reset" state, not a
			// failure — the system is already in the default state.
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				writeError(w, http.StatusInternalServerError, "failed to reset system prompt")
				return
			}
		}
		writeJSON(w, http.StatusOK, systemPromptResponse{SystemPrompt: defaultSystemPrompt()})
		return
	}

	if err := h.db.Settings.Set(uid, settingKeySystemPrompt, prompt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save system prompt")
		return
	}
	writeJSON(w, http.StatusOK, systemPromptResponse{SystemPrompt: prompt})
}
