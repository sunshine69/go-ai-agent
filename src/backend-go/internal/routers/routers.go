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
//   - model.go         GET /api/model/status, POST /api/model/set
//
// Note: skills is intentionally omitted per project decision.
package routers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/config"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/llm"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/mcpclient"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/ragmanager"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/ragstore"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/serving"
)

// Handlers bundles the dependencies shared by all handlers. It is passed through
// to each handler struct so routing logic stays uniform.
type Handlers struct {
	// mcp resolves the active MCP server per caller at request time. Each
	// handler stores a copy of Handlers, so this pointer is read in the
	// handler's own goroutine against the authenticated request.
	mcp *mcpclient.MCPManager

	// ragManager resolves the active RAG store per caller at request time.
	// Each handler stores a copy of Handlers, so this pointer is read in the
	// handler's own goroutine against the authenticated request.
	ragManager *ragmanager.Manager

	// LLM is the OpenAI-compatible client used by the messages handler.
	LLM *llm.Client
	// Rag is the vector store used by the messages handler (fallback when no
	// per-user override is configured).
	Rag *ragstore.RAGStore
	// Config holds the resolved configuration for building the context builder.
	Cfg *config.Config

	// Frontend serves the built SPA at the /frontend/* prefix. When nil or
	// disabled the /frontend/* endpoints are omitted from ServeMux.

	// DB is the application datastore (users, conversations, messages).
	// When nil the DB-backed endpoints (auth, conversations) are disabled.
	DB       *db.DB
	Frontend *serving.Server
}

// NewHandlers builds a Handlers with an MCP manager bound to a shared default
// (possibly nil) client. It exists so callers outside the package (e.g. main) can
// construct Handlers without touching the unexported mcp field.
func NewHandlers(mcpManager *mcpclient.MCPManager, llmClient *llm.Client, rag *ragstore.RAGStore, ragManager *ragmanager.Manager, cfg *config.Config, db *db.DB, frontend *serving.Server) Handlers {
	return Handlers{
		mcp:        mcpManager,
		ragManager: ragManager,
		LLM:        llmClient,
		Rag:        rag,
		Cfg:        cfg,
		DB:         db,
		Frontend:   frontend,
	}

}

// Cors returns the effective CORS settings for the middleware, sourced from the
// backend configuration.
func (h Handlers) Cors() config.Cors {
	return h.Cfg.Cors()
}

// mcpClient returns the MCP client bound to this request's authenticated caller.
// When the caller cannot be resolved (no/invalid token) it returns the shared
// default, mirroring the pre-multi-user behaviour of serving every request from
// the single process-wide server. A nil return means "no MCP server connected" —
// callers must nil-guard their reads.
func (h *Handlers) mcpClient(r *http.Request) *mcpclient.ResilientMCPClient {
	if h.mcp == nil {
		return nil
	}
	if uid, ok := currentUserID(r); ok {
		return h.mcp.Client(uid)
	}
	return h.mcp.Default()
}

// applyUserCtxLimit returns a config suitable for the request's authenticated
// caller, honouring their stored per-user ctxLimit setting (via /ctx). When the
// user has no valid setting it returns cfg unchanged. It is nil-safe.
func (h *Handlers) applyUserCtxLimit(cfg *config.Config, r *http.Request) *config.Config {
	if cfg == nil {
		return cfg
	}
	if h.DB == nil || h.DB.Settings == nil {
		return cfg
	}
	if uid, ok := currentUserID(r); !ok {
		return cfg
	} else if n, valid := resolveCtxLimit(h.DB, uid); valid {
		out := *cfg
		out.ContextLimit = n
		return &out
	}
	return cfg
}

// applyUserLLMURL returns the effective LLM base-URL for the request's
// authenticated caller, honouring their stored per-user override (via /url)
// over the configured default. When the user has no override (or a reset value)
// it returns cfg.LLMBASEURL unchanged. It is nil-safe and MUST be applied where
// the final ".../chat/completions" endpoint is built, so a user's /url choice
// actually shapes the server a later message streams from.
func (h *Handlers) applyUserLLMURL(cfg *config.Config, r *http.Request) string {
	if cfg == nil {
		return ""
	}
	base := cfg.LLMBASEURL
	if h.DB == nil || h.DB.Settings == nil {
		return base
	}
	uid, ok := currentUserID(r)
	if !ok {
		return base
	}
	override := resolveLLMURL(h.DB, uid)
	if override != "" {
		return override
	}
	log.Printf("[LLMURL] applyUserLLMURL uid=%d cfg=%q override=%q -> base=%q", uid, cfg.LLMBASEURL, override, base)
	return base
}

// applyUserLLMModel returns a clone of the LLM client that honours the
// caller's stored per-user model name override (via /m) over the configured
// default (cfg.LLMModel). When the user has no override it returns the client
// unchanged. It is nil-safe. Callers MUST use the returned client for every
// LLM call in the request path so a user's /m choice shapes the model a later
// message is served from, and the choice persists across backend restarts
// because it is stored in the user_settings table.
func (h *Handlers) applyUserLLMModel(client *llm.Client, r *http.Request) *llm.Client {
	if client == nil {
		return client
	}
	if h.DB == nil || h.DB.Settings == nil {
		return client
	}
	uid, ok := currentUserID(r)
	if !ok {
		return client
	}
	model := resolveModelName(h.DB, uid)
	if model == "" {
		return client
	}
	log.Printf("[MODEL] applyUserLLMModel uid=%d cfg=%q override=%q -> effective=%q",
		uid, client.Model(), model, model)
	return client.WithModel(model)
}
// ServeMux builds the router for the backend: the /api/* handlers plus, when
// configured, the /frontend/* SPA static file server.
func (h Handlers) ServeMux() http.Handler {
	mux := http.NewServeMux()

	domains := newDomainsHandler()
	messages := newMessagesHandler(h)
	messagesStream := newMessagesStreamHandler(h)
	conversations := newConversationsHandler(h.DB)
	confluence := newConfluenceHandler(h)
	documents := newDocumentsHandler(h)
	forms := newFormsHandler(h)
	processes := newProcessesHandler(h)
	auth := newAuthHandler(h.DB)
	mcp := newMCPHandler(h)
	mcpdir := newMCPdirHandler(h.DB)
	ragdir := newRagdirHandler(h.DB, h.ragManager)
	settings := newSettingsHandler(h.DB)
	systemPrompt := newSystemPromptHandler(h.DB)
	llmURL := newLLMURLHandler(h.DB)
	model := newModelHandler(h)
	mux.HandleFunc("/api/domains", requireAuth(domains.handle))
	mux.HandleFunc("/api/messages", requireAuth(messages.handle))
	mux.HandleFunc("/api/messages/stream", requireAuth(messagesStream.proxyLLMStream))    // Direct proxy to LLM
	mux.HandleFunc("/api/chat/stream", requireAuth(messagesStream.handleStreamChat))      // Alternative SSE format endpoint
	mux.HandleFunc("/api/conversations", conversations.handleListAndCreate)               // GET (list), POST (create)
	mux.HandleFunc("DELETE /api/conversations", conversations.handleClearAll)             // DELETE (clear all) — no trailing slash, method-specific pattern wins
	mux.HandleFunc("POST /api/conversations/bulk-delete", conversations.handleDeleteMany) // multi-select delete
	mux.HandleFunc("/api/conversations/", conversations.handleByID)                       // GET, DELETE /{id} (a single conversation)
	mux.HandleFunc("/api/confluence/search", confluence.handleSearch)
	mux.HandleFunc("/api/documents/", documents.handle)
	mux.HandleFunc("/api/forms", forms.handle)
	mux.HandleFunc("/api/processes/", processes.handle)
	mux.HandleFunc("/api/auth/register", auth.handleRegister)
	mux.HandleFunc("/api/auth/login", auth.handleLogin)
	mux.HandleFunc("/api/auth/me", auth.handleMe)
	// --- New DB-backed auth endpoints (multi-user) ---
	mux.HandleFunc("/api/auth/logout", auth.handleLogout)                  // POST
	mux.HandleFunc("GET /api/auth/users", auth.handleUsers)                // GET (list)
	mux.HandleFunc("POST /api/auth/users", auth.handleCreateUser)          // POST (create)
	mux.HandleFunc("/api/auth/users/", auth.handleDeleteUser)              // DELETE /{id}
	mux.HandleFunc("GET /api/auth/me/profile", auth.handleProfile)         // GET (read)
	mux.HandleFunc("PATCH /api/auth/me/profile", auth.handleProfileUpdate) // PATCH
	// --- Per-user settings (supports the /ctx and /help commands) ---
	mux.HandleFunc("GET /api/mcp", requireAuth(mcp.handleStatus))
	mux.HandleFunc("POST /api/mcp", requireAuth(mcp.handleConnect))
	mux.HandleFunc("GET /api/settings", requireAuth(settings.handleList))
	mux.HandleFunc("POST /api/settings", requireAuth(settings.handleSet))
	mux.HandleFunc("/api/auth/me/profile/password", auth.handlePasswordChange) // POST
	mux.HandleFunc("/api/model/status", model.handleStatus)
	mux.HandleFunc("/api/model/set", model.handleSet)

	mux.HandleFunc("GET /api/mcpdir", requireAuth(mcpdir.handleList))
	mux.HandleFunc("POST /api/mcpdir", requireAuth(mcpdir.handleSet))

	mux.HandleFunc("GET /api/ragdir", requireAuth(ragdir.handleList))
	mux.HandleFunc("GET /api/system", requireAuth(systemPrompt.handleList))
	mux.HandleFunc("POST /api/system", requireAuth(systemPrompt.handleSet))
	mux.HandleFunc("GET /api/llmurl", requireAuth(llmURL.handleList))
	mux.HandleFunc("POST /api/llmurl", requireAuth(llmURL.handleSet))
	mux.HandleFunc("POST /api/ragdir", requireAuth(ragdir.handleSet))
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
