// Package routers — model.go: per-user model selector endpoints
// (GET /api/model/status, POST /api/model/set), backed by the per-user
// user_settings table so the choice survives a backend restart.
//
// The `/m` slash command lets a user switch the active model mid-session, so a
// later message streams from a different model without a restart. The current
// model is reported by GET /api/model/status and changed by POST /api/model/set.
//
// Persistence: the chosen model is stored in user_settings(key='model_name')
// for the authenticated caller, exactly like the /url and /ctx per-user
// settings. On backend restart the value is read back from the DB on every
// request via applyUserLLMModel, so it is never lost.
package routers

import (
	"net/http"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
)

// modelStatusResponse is the client-facing view of the active model name.
type modelStatusResponse struct {
	Model string `json:"model"`
}

// modelHandler serves the model status/set endpoints, backed by the per-user
// user_settings table.
type modelHandler struct {
	dbStore *db.DB
}

func newModelHandler(h Handlers) *modelHandler {
	return &modelHandler{dbStore: h.DB}
}

// handleModelStatus serves GET /api/model/status — reports the model in effect
// for the authenticated user. It returns the per-user override if one is set,
// falling back to the configured default.
func (h *modelHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	model := resolveModelName(h.dbStore, uid)
	writeJSON(w, http.StatusOK, modelStatusResponse{Model: model})
}

// handleModelSet serves POST /api/model/set — overrides the active model for
// the authenticated user. Body: {"model": "gpt-4o"}. A blank model name is a
// no-op. The value is persisted in user_settings(key='model_name') so it
// survives a backend restart.
func (h *modelHandler) handleSet(w http.ResponseWriter, r *http.Request) {
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
		Model string `json:"model"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	model := strings.TrimSpace(req.Model)
	if model != "" && h.dbStore != nil && h.dbStore.Settings != nil {
		if err := h.dbStore.Settings.Set(uid, "model_name", model); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save model name: "+err.Error())
			return
		}
	}
	// Return the effective model (per-user override if set, else default).
	effective := resolveModelName(h.dbStore, uid)
	writeJSON(w, http.StatusOK, modelStatusResponse{Model: effective})
}
