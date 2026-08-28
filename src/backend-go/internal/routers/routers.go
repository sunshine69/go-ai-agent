// Package routers exposes the HTTP layer of the Go backend. It mirrors the
// Python FastAPI routers under /api/*, returning byte-compatible response shapes
// that the Wails frontend depends on. The sub-routers are:
//
//   - domains.go       GET /api/domains
//   - messages.go      POST /api/messages  (the brain: context + LLM)
//   - conversations.go GET/POST/DELETE /api/conversations[/{id}]
//   - confluence.go    GET /api/confluence/search
//   - documents.go     GET /api/documents[/{category}|/{id}/content]
//   - forms.go         GET /api/forms
//   - processes.go     GET /api/processes[/{id}|/{id}/owner]
//   - auth.go          POST /api/auth/register|login, GET /api/auth/me
//
// Note: skills is intentionally omitted per project decision.
package routers

import (
	"encoding/json"
	"net/http"

	"github.com/stevek/go-ai-agent/backend-go/internal/config"
	"github.com/stevek/go-ai-agent/backend-go/internal/llm"
	"github.com/stevek/go-ai-agent/backend-go/internal/mcpclient"
	"github.com/stevek/go-ai-agent/backend-go/internal/ragstore"
)

// Handlers bundles the dependencies shared by all handlers. It is passed through
// to each handler struct so routing logic stays uniform.
type Handlers struct {
	Manager *mcpclient.Manager

	// LLM is the OpenAI-compatible client used by the messages handler.
	LLM *llm.Client
	// Rag is the vector store used by the messages handler.
	Rag *ragstore.RAGStore
	// Config holds the resolved configuration for building the context builder.
	Cfg *config.Config
}

// ServeMux builds the /api/* mux for the backend. The routes mirror the Python
// main.py mounts exactly; no endpoint-shape changes are introduced here.
func (h Handlers) ServeMux() *http.ServeMux {
	mux := http.NewServeMux()

	domains := newDomainsHandler()
	messages := newMessagesHandler(h)
	conversations := newConversationsHandler()
	confluence := newConfluenceHandler(h)
	documents := newDocumentsHandler(h)
	forms := newFormsHandler(h)
	processes := newProcessesHandler(h)
	auth := newAuthHandler()

	mux.HandleFunc("/api/domains", domains.handle)
	mux.HandleFunc("/api/messages", messages.handle)
	mux.HandleFunc("/api/conversations", conversations.handleListAndCreate)
	mux.HandleFunc("/api/conversations/", conversations.handleByID)
	mux.HandleFunc("/api/confluence/search", confluence.handleSearch)
	mux.HandleFunc("/api/documents/", documents.handle)
	mux.HandleFunc("/api/forms", forms.handle)
	mux.HandleFunc("/api/processes/", processes.handle)
	mux.HandleFunc("/api/auth/register", auth.handleRegister)
	mux.HandleFunc("/api/auth/login", auth.handleLogin)
	mux.HandleFunc("/api/auth/me", auth.handleMe)

	return mux
}

// writeJSON marshals v and writes it with the proper content type and status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// writeError writes a JSON error body matching the Python FastAPI shape
// {"detail": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"detail": msg})
}

// decodeBody reads a JSON request body into v, returning an error response if the
// body is malformed.
func decodeBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}
