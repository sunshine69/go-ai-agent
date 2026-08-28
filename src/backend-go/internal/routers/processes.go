package routers

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

type processesHandler struct {
	h Handlers
}

func newProcessesHandler(h Handlers) *processesHandler {
	return &processesHandler{h: h}
}

// processesPath mirrors the Python processes.py get_processes_path(): resolve
// resources/documents/skills/processes.json relative to MCP_WORK_DIR (absolute)
// or, if unset, walk up from the module file's own location.
func processesPath() string {
	if wd := os.Getenv("MCP_WORK_DIR"); wd != "" && strings.HasPrefix(wd, "/") {
		return join(wd, "resources", "documents", "skills", "processes.json")
	}
	// Walk up from src/backend-go/internal/routers/ to the project root.
	dir := "."
	return join(dir, "resources", "documents", "skills", "processes.json")
}

func join(elems ...string) string {
	var b strings.Builder
	for _, e := range elems {
		e = strings.TrimSpace(e)
		if e != "" {
			b.WriteString(e)
			b.WriteString("/")
		}
	}
	s := b.String()
	return strings.TrimSuffix(s, "/")
}

// loadProcesses reads the processes.json, tolerating both the flat list
// format and the wrapper {"processes": [...]} format.
func loadProcesses() []map[string]interface{} {
	path := processesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// Flat format: [...]
	if strings.HasPrefix(strings.TrimSpace(string(data)), "[") {
		var arr []map[string]interface{}
		if err := json.Unmarshal(data, &arr); err == nil {
			return arr
		}
		return nil
	}

	// Wrapper format: {"processes": [...]}
	var wrapper struct {
		Processes []map[string]interface{} `json:"processes"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil {
		return wrapper.Processes
	}
	return nil
}

// handle serves:
//
//	GET /api/processes               -> list
//	GET /api/processes/search?keyword=X -> search
//	GET /api/processes/{id}          -> process detail
//	GET /api/processes/{id}/owner    -> owner info
func (h *processesHandler) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/processes" || r.URL.Path == "/api/processes/":
		h.list(w, r)
		return
	case strings.HasPrefix(r.URL.Path, "/api/processes/search"):
		h.search(w, r)
		return
	}

	trimmed := strings.Trim(r.URL.Path, "/")
	if idx := strings.Index(trimmed, "/owner"); idx >= 0 {
		id := trimmed[:idx]
		h.getOwner(w, id)
		return
	}
	// /api/processes/{id}
	id := trimmed
	h.get(w, id)
}

func (h *processesHandler) list(w http.ResponseWriter, r *http.Request) {
	procs := loadProcesses()
	if procs == nil {
		procs = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, procs)
}

func (h *processesHandler) search(w http.ResponseWriter, r *http.Request) {
	keyword := strings.ToLower(r.URL.Query().Get("keyword"))
	procs := loadProcesses()
	results := []map[string]interface{}{}
	for _, p := range procs {
		name, _ := p["name"].(string)
		desc, _ := p["description"].(string)
		if strings.Contains(strings.ToLower(name), keyword) || strings.Contains(strings.ToLower(desc), keyword) {
			results = append(results, p)
		}
	}
	writeJSON(w, http.StatusOK, results)
}

func (h *processesHandler) get(w http.ResponseWriter, id string) {
	procs := loadProcesses()
	for _, p := range procs {
		if pid, _ := p["process_id"].(string); pid == id {
			writeJSON(w, http.StatusOK, p)
			return
		}
	}
	writeError(w, http.StatusNotFound, "Process not found")
}

func (h *processesHandler) getOwner(w http.ResponseWriter, id string) {
	procs := loadProcesses()
	var found bool
	for _, p := range procs {
		if pid, _ := p["process_id"].(string); pid == id {
			found = true
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"process_id":  id,
				"owner":       strOrEmpty(p, "owner"),
				"owner_title": strOrEmpty(p, "owner_title"),
				"owner_email": strOrEmpty(p, "owner_email"),
			})
			return
		}
	}
	if found {
		writeError(w, http.StatusNotFound, "Process not found")
		return
	}
	// Python raises "Processes data not found" when the file is missing.
	writeError(w, http.StatusNotFound, "Processes data not found")
}

func strOrEmpty(m map[string]interface{}, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}
