package routers

import (
	"net/http"
	"regexp"
	"strings"
)

// formResult mirrors the shape produced by parse_forms_text.
type formResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Path     string `json:"path"`
}

var (
	formRegex = regexp.MustCompile(`^##\s+(.+?)\s+\(ID:\s+(.+?)\)\s*\nCategory:\s+(.+?)\s*\nPath:\s+(.+?)(?:\n|$)`)
)

type formsHandler struct {
	h Handlers
}

func newFormsHandler(h Handlers) *formsHandler {
	return &formsHandler{h: h}
}

// handle serves GET /api/forms[?q=KEYWORD].
func (c *formsHandler) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")

	if c.h.Manager == nil {
		writeError(w, http.StatusInternalServerError, "MCP manager unavailable")
		return
	}

	var toolName string
	var args map[string]interface{}
	if q != "" {
		toolName = "forms_search"
		args = map[string]interface{}{"keyword": q}
	} else {
		toolName = "forms_list"
	}

	result, err := c.h.Manager.CallTool(toolName, args)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MCP error: "+err.Error())
		return
	}

	forms := parseFormsText(result)
	if forms == nil {
		forms = []formResult{}
	}
	writeJSON(w, http.StatusOK, forms)
}

// parseFormsText parses "## Title (ID: id)\nCategory: cat\nPath: path" lines.
func parseFormsText(text string) []formResult {
	matches := formRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	forms := make([]formResult, 0, len(matches))
	for _, m := range matches {
		title := strings.TrimSpace(text[m[2]:m[3]])
		id := strings.TrimSpace(text[m[4]:m[5]])
		category := strings.TrimSpace(text[m[6]:m[7]])
		path := strings.TrimSpace(text[m[8]:m[9]])
		forms = append(forms, formResult{
			ID:       id,
			Title:    title,
			Category: category,
			Path:     path,
		})
	}
	return forms
}
