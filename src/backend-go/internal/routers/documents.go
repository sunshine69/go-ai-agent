package routers

import (
	"net/http"
	"regexp"
	"strings"
)

// documentResult mirrors the shape produced by parse_documents_text.
type documentResult struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Category    string   `json:"category"`
	Path        string   `json:"path"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

var (
	// docRegex matches "## Title (ID: id)\nCategory: cat\nPath: path" lines.
	docRegex = regexp.MustCompile(`^##\s+(.+?)\s+\(ID:\s+(.+?)\)\s*\nCategory:\s+(.+?)\s*\nPath:\s+(.+?)(?:\n|$)`)
)

type documentsHandler struct {
	h Handlers
}

func newDocumentsHandler(h Handlers) *documentsHandler {
	return &documentsHandler{h: h}
}

// handle serves:
//
//	GET /api/documents            (list all)
//	GET /api/documents?collection=X
//	GET /api/documents/{category}
//	GET /api/documents/{id}/content
func (c *documentsHandler) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	collection := r.URL.Query().Get("collection")

	switch {
	case path == "/api/documents" || path == "/api/documents/":
		if collection != "" {
			c.listCategory(w, collection)
		} else {
			c.listAll(w)
		}
		return
	}

	trimmed := strings.Trim(path, "/")
	if strings.HasSuffix(trimmed, "/content") {
		parts := strings.SplitN(trimmed, "/content", 2)
		id := parts[0]
		c.getContent(w, r, id)
		return
	}

	// /api/documents/{category}
	c.listCategory(w, trimmed)
}

func (c *documentsHandler) listAll(w http.ResponseWriter) {
	result := c.documentsList(nil, w, nil)
	if result == "" {
		return
	}
	docs := parseDocumentsText(result)
	if len(docs) == 0 {
		docs = parseCollectionsText(result)
	}
	if docs == nil {
		docs = []documentResult{}
	}
	writeJSON(w, http.StatusOK, docs)
}

func (c *documentsHandler) listCategory(w http.ResponseWriter, category string) {
	result := c.documentsList(nil, w, map[string]interface{}{"collection": category})
	if result == "" {
		return
	}
	docs := parseDocumentsText(result)
	if len(docs) == 0 {
		docs = parseCollectionsText(result)
	}
	if docs == nil {
		docs = []documentResult{}
	}
	writeJSON(w, http.StatusOK, docs)
}

func (c *documentsHandler) getContent(w http.ResponseWriter, r *http.Request, id string) {
	client := c.h.mcpClient(r)
	if client == nil {
		writeError(w, http.StatusInternalServerError, "MCP manager unavailable")
		return
	}
	result, err := client.CallTool("documents_get_content", map[string]interface{}{"document_id": id})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MCP error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": result})
}

func (c *documentsHandler) documentsList(r *http.Request, w http.ResponseWriter, args map[string]interface{}) string {
	client := c.h.mcpClient(r)
	if client == nil {
		writeError(w, http.StatusInternalServerError, "MCP manager unavailable")
		return ""
	}
	result, err := client.CallTool("documents_list", args)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MCP error: "+err.Error())
		return ""
	}
	return result
}

// parseDocumentsText parses "## Title (ID: id)\nCategory: cat\nPath: path" lines.
func parseDocumentsText(text string) []documentResult {
	matches := docRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil
	}
	docs := make([]documentResult, 0, len(matches))
	for _, m := range matches {
		title := strings.TrimSpace(text[m[2]:m[3]])
		id := strings.TrimSpace(text[m[4]:m[5]])
		category := strings.TrimSpace(text[m[6]:m[7]])
		path := strings.TrimSpace(text[m[8]:m[9]])
		// Description if present immediately after the path line.
		description, tags := parseDescriptionAndTags(text, m[9])
		docs = append(docs, documentResult{
			ID:          id,
			Title:       title,
			Category:    category,
			Path:        path,
			Description: description,
			Tags:        tags,
		})
	}
	return docs
}

// parseCollectionsText handles collection headers ("### name\n- Documents: N\n- Path: x").
// For the common MCP layout (flat list of documents), this falls back to the
// document regex. The Python parser builds collection-scoped ids; here we reuse
// the document parser since the MCP server emits a flat document list.
func parseCollectionsText(text string) []documentResult {
	return parseDocumentsText(text)
}

// parseDescriptionAndTags looks for Description/Tags lines following a doc match.
func parseDescriptionAndTags(text string, afterIndex int) (string, []string) {
	rest := text[afterIndex:]
	// Match "Description: ...\n"
	var desc string
	if d := firstLine(rest); strings.HasPrefix(d, "Description:") {
		desc = strings.TrimSpace(strings.TrimPrefix(d, "Description:"))
		rest = trimFirstLine(rest)
	}
	var tags []string
	if t := firstLine(rest); strings.HasPrefix(t, "Tags:") {
		tagStr := strings.TrimSpace(strings.TrimPrefix(t, "Tags:"))
		if tagStr != "" {
			for _, p := range strings.Split(tagStr, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					tags = append(tags, p)
				}
			}
		}
	}
	return desc, tags
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func trimFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		if i+1 < len(s) && s[i+1] == '\r' {
			return s[i+2:]
		}
		return s[i+1:]
	}
	return ""
}
