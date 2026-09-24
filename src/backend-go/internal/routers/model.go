// Package routers — model.go: per-call model selector endpoints
// (GET /api/model/status, POST /api/model/set), backed by the runtime model
// override on the shared llm.Client.
//
// The `/m` slash command lets a user switch the active model mid-session, so a
// later message streams from a different model without a restart. The current
// model is reported by GET /api/model/status and changed by POST /api/model/set.
package routers

import (
	"net/http"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/llm"
)

// modelStatusResponse is the client-facing view of the active model name.
type modelStatusResponse struct {
	Model string `json:"model"`
}

// modelHandler serves the model status/set endpoints.
type modelHandler struct {
	llm *llm.Client
}

func newModelHandler(h Handlers) *modelHandler {
	return &modelHandler{llm: h.LLM}
}

// handleModelStatus serves GET /api/model/status — reports the model in effect.
func (h *modelHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	_, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	model := ""
	if h.llm != nil {
		model = h.llm.Model()
	}
	writeJSON(w, http.StatusOK, modelStatusResponse{Model: model})
}

// handleModelSet serves POST /api/model/set — overrides the active model.
// Body: {"model": "gpt-4o"}. A blank model is a no-op.
func (h *modelHandler) handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	_, ok := currentUserID(r)
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
	if h.llm != nil {
		h.llm.SetModel(req.Model)
	}
	model := ""
	if h.llm != nil {
		model = h.llm.Model()
	}
	writeJSON(w, http.StatusOK, modelStatusResponse{Model: model})
}
