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
	"strconv"

	"github.com/stevek/go-ai-agent/backend-go/internal/config"
	"github.com/stevek/go-ai-agent/backend-go/internal/llm"
	"github.com/stevek/go-ai-agent/backend-go/internal/mcpclient"
	"github.com/stevek/go-ai-agent/backend-go/internal/ragstore"
	"github.com/stevek/go-ai-agent/backend-go/internal/serving"
)

// Handlers bundles the dependencies shared by all handlers. It is passed through
// to each handler struct so routing logic stays uniform.
type Handlers struct {
	Manager *mcpclient.ResilientMCPClient

	// LLM is the OpenAI-compatible client used by the messages handler.
	LLM *llm.Client
	// Rag is the vector store used by the messages handler.
	Rag *ragstore.RAGStore
	// Config holds the resolved configuration for building the context builder.
	Cfg *config.Config

	// Frontend serves the built SPA at the /frontend/* prefix. When nil or
	// disabled the /frontend/* endpoints are omitted from ServeMux.
	Frontend *serving.Server
}

// Cors returns the effective CORS settings for the middleware, sourced from the
// backend configuration.
func (h Handlers) Cors() config.Cors {
	return h.Cfg.Cors()
}

// ServeMux builds the router for the backend: the /api/* handlers plus, when
// configured, the /frontend/* SPA static file server.
func (h Handlers) ServeMux() http.Handler {
	mux := http.NewServeMux()

	domains := newDomainsHandler()
	messages := newMessagesHandler(h)
	messagesStream := newMessagesStreamHandler(h)
	conversations := newConversationsHandler()
	confluence := newConfluenceHandler(h)
	documents := newDocumentsHandler(h)
	forms := newFormsHandler(h)
	processes := newProcessesHandler(h)
	auth := newAuthHandler()

	mux.HandleFunc("/api/domains", domains.handle)
	mux.HandleFunc("/api/messages", messages.handle)
	mux.HandleFunc("/api/messages/stream", messagesStream.proxyLLMStream) // Direct proxy to LLM
	mux.HandleFunc("/api/chat/stream", messagesStream.handleStreamChat)   // Alternative SSE format endpoint
	mux.HandleFunc("/api/conversations", conversations.handleListAndCreate)
	mux.HandleFunc("/api/conversations/", conversations.handleByID)
	mux.HandleFunc("/api/confluence/search", confluence.handleSearch)
	mux.HandleFunc("/api/documents/", documents.handle)
	mux.HandleFunc("/api/forms", forms.handle)
	mux.HandleFunc("/api/processes/", processes.handle)
	mux.HandleFunc("/api/auth/register", auth.handleRegister)
	mux.HandleFunc("/api/auth/login", auth.handleLogin)
	mux.HandleFunc("/api/auth/me", auth.handleMe)

	// Serve the SPA (if configured) at /frontend/* before the /api/* mux, so
	// frontend requests are handled by the static file server rather than the
	// child mux. Registered on the top-level mux only (not on `mux`, the /api
	// sub-mux) so it always wins for the /frontend subtree.
	if h.Frontend != nil {
		mux.Handle(serving.Prefix, h.Frontend.Handler())
		// Register the trailing-slash-less form too. Go 1.21+ pattern mux does
		// not match a subtree pattern to its own prefix without the slash, so we
		// redirect /frontend -> /frontend/ and let the subtree handler serve it.
		mux.HandleFunc(serving.FrontendPath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, serving.Prefix, http.StatusFound)
		})
	}

	return corsMiddleware(http.Handler(mux), h.Cors())
}

// CORS middleware wraps a handler so browser-based frontends (notably the Wails
// desktop WebView at origin "wails://") can read cross-origin responses from the
// /api/* endpoints served on the same port. Without it, a Wails fetch to
// localhost:8000 fails the CORS preflight (405) and the body is unreadable,
// producing "Load failed" and "New Conversation" doing nothing.
//
// It is intentionally minimal and permissive so it never alters the shape or
// behaviour of the endpoints it wraps. When enabled is false the handler passes
// straight through so no CORS headers are emitted at all.
func corsMiddleware(next http.Handler, cors config.Cors) http.Handler {
	// When disabled, pass straight through so no CORS headers are emitted at
	// all (useful for server-side-only clients or locking the backend down).
	if !cors.Enabled {
		return next
	}

	// Allow-Origin header: "" is treated as a wildcard ("*"); ":" is a
	// sentinel meaning "reflect the request origin" (needed for the
	// Access-Control-Allow-Credentials: true case, where a bare "*" is not
	// allowed by browsers).
	originHeader := cors.Origin
	if originHeader == "" {
		originHeader = "*"
	}
	if originHeader == ":" && cors.AllowCreds {
		originHeader = "" // trigger origin reflection below
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Credentials + wildcard aren't allowed together: when a specific
		// origin is requested we reflect the request's Origin back verbatim.
		if originHeader == "" {
			if o := r.Header.Get("Origin"); o != "" {
				originHeader = o
			}
		}

		w.Header().Set("Access-Control-Allow-Origin", originHeader)
		if cors.AllowCreds {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Methods", cors.Methods)
		w.Header().Set("Access-Control-Allow-Headers", cors.AllowHeaders)
		if cors.MaxAge > 0 {
			w.Header().Set("Access-Control-Max-Age", strconv.Itoa(cors.MaxAge))
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
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
