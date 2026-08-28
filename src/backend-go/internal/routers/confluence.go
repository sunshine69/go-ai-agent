package routers

import (
	"net/http"
)

type confluenceHandler struct {
	h Handlers
}

func newConfluenceHandler(h Handlers) *confluenceHandler {
	return &confluenceHandler{h: h}
}

// handleSearch serves GET /api/confluence/search?q= by delegating to the
// confluence_search MCP tool. The Python backend returns {"results": <text>},
// so we echo the raw tool output text unchanged.
func (c *confluenceHandler) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")

	if c.h.Manager == nil {
		writeError(w, http.StatusInternalServerError, "MCP manager unavailable")
		return
	}

	result, err := c.h.Manager.CallTool("confluence_search", map[string]interface{}{
		"keyword": q,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MCP error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"results": result})
}
