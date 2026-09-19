// Package routers — mcp.go: reports the current MCP server connection state to
// the SPA so the /mcp slash command in the chat input can render a live status
// (connected server, available tool list) instead of guessing. It also exposes
// POST /api/mcp so the user can launch or disconnect a server at runtime — the
// connect path mirrors the /ctx and /help commands' small, well-defined set of
// back-end facts and the reference CLI's /mcp handler.
//
// Per-user connections are stored per caller in Handlers.mcp (an
// mcpclient.MCPManager). This file reads the shared default connection for the
// status view and writes the caller's own override on connect/disconnect, so a
// per-user server never disrupts another user's session.
package routers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/mcpclient"
)

// mcpStatusResponse is the client-facing view of the MCP connection. It is sent
// to the SPA from GET /api/mcp with no request body.
type mcpStatusResponse struct {
	// Connected reports whether a live MCP server is attached. When false the
	// spec and tools arrays are empty and the SPA shows a "not connected" hint.
	Connected bool `json:"connected"`
	// Spec is the launch spec of the connected server (stdio argv or HTTP URL).
	Spec string `json:"spec,omitempty"`
	// Tools lists the currently available MCP tools so the user can see what the
	// model can call. Empty when no server is connected.
	Tools []mcpToolSummary `json:"tools"`
	// Error carries a human-readable diagnostic when MCP was requested but the
	// manager failed to initialise (best-effort; may be empty).
	Error string `json:"error,omitempty"`
}

// mcpToolSummary is the minimal tool descriptor shown in the chat UI — just
// enough for a human to recognise what a tool does without the full JSON schema.
type mcpToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// mcpHandler serves the read-only MCP status endpoint.
type mcpHandler struct {
	h Handlers
}

func newMCPHandler(h Handlers) *mcpHandler {
	return &mcpHandler{h: h}
}

// handleStatus serves GET /api/mcp. It never connects or disconnects a server;
// it reports the caller's effective MCP connection — resolving their per-user
// override and falling back to the shared default, exactly like the tool-serving
// path. A caller who has run `/mcp off` is honoured here too (see
// MCPManager.Client, which returns nil for an explicitly disconnected user),
// rather than showing the process-wide default which stays connected for the
// life of the process. When the effective client is nil (MCP disabled,
// initialisation failed, or the caller has disconnected) it still reports
// connected = false so the SPA can show an actionable hint rather than a bare
// "unknown".
func (m *mcpHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := mcpStatusResponse{Connected: false}

	def := m.h.mcpClient(r)
	if def != nil {
		resp.Connected = true
		resp.Spec = def.Spec
		for _, t := range def.Tools() {
			resp.Tools = append(resp.Tools, mcpToolSummary{
				Name:        t.Name,
				Description: t.Description,
			})
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// mcpConnectRequest is the request body for POST /api/mcp:
//
//	{"action":"connect","spec":"node ./server.js --port 3000"}
//	{"action":"disconnect"}
//
// The spec is whitespace-split on the backend; use /mcp off to disconnect.
type mcpConnectRequest struct {
	// Action is "connect" or "disconnect".
	Action string `json:"action"`
	// Spec is the launch spec for "connect": an HTTP URL (Streamable HTTP) or a
	// whitespace-separated stdio command. Ignored for "disconnect".
	Spec string `json:"spec"`
	// WorkDir is an optional working directory the stdio MCP child runs in. It
	// must be a relative path with no ".." component (validated server-side and
	// created if missing). For stdio connections only; ignored for HTTP specs.
	// When omitted the child uses the backend process cwd.
	WorkDir string `json:"workdir,omitempty"`
}

// handleConnect serves POST /api/mcp. Unlike handleStatus it mutates state:
//
//   - action "connect": builds a ResilientMCPClient from spec (Streamable HTTP for
//     http(s):// specs, whitespace-split stdio otherwise), then stores it in the
//     caller's per-user override in Handlers.mcp. The old override for that user
//     (if any) is closed in place before the new one is stashed.
//
//   - action "disconnect": drops the caller's override (Set(uid, nil)), closing
//     any live inner client. The nil-guarded Tools()/CallTool()/Resources()/
//     ReadResource() report "no MCP server connected" instead of panicking.
func (m *mcpHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req mcpConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	switch req.Action {
	case "connect":
		// Per-user MCP gate: admins and users with allow_mcp=1 may connect;
		// everyone else is denied a live MCP connection. This is applied in the
		// handler (requireAuth only guarantees a valid token) so the DB flag is the
		// authoritative check — and it can be toggled per account over time.
		uid, ok := currentUserID(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if m.h.DB == nil {
			writeError(w, http.StatusInternalServerError, "permission service unavailable")
			return
		}
		caller, err := m.h.DB.Users.GetByID(uid)
		if err != nil || caller == nil {
			writeError(w, http.StatusForbidden, "access required")
			return
		}
		if !caller.IsAdmin && !caller.AllowMcp {
			writeError(w, http.StatusForbidden, "MCP access is not permitted for your account")
			return
		}
		if strings.TrimSpace(req.Spec) == "" {
			writeError(w, http.StatusBadRequest, "spec is required for connect")
			return
		}
		// Prefer an explicit workdir from the request; otherwise honour the
		// caller's stored /mcpdir setting so a later /mcp uses that directory.
		workdir := req.WorkDir
		if strings.TrimSpace(workdir) == "" {
			if wd, ok := mcpclient.DefaultMCPWorkdir(m.h.DB, uid); ok {
				workdir = wd
			}
		}
		manager, err := mcpclient.Connect(req.Spec, m.managerBlockList(), workdir)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to connect MCP: "+err.Error())
			return
		}
		// Per-user write: stash this user's client in their own override,
		// replacing any prior override in place (closing the old child). This is
		// request-scoped, so it never disrupts another user's connection.
		uid = currentUserIDOr(r, 0)
		m.h.mcp.Set(uid, manager)

		// Report the caller's effective MCP connection — what they just connected.
		def := m.h.mcpClient(r)
		writeJSON(w, http.StatusOK, mcpStatusResponse{
			Connected: def != nil,
			Spec:      def.Spec,
			Tools:     toolsFromManager(def),
			Error:     "",
		})

	case "disconnect":
		// Per-user write: drop this user's override (closing any live child),
		// restoring their per-user default. A user with no override is a no-op.
		uid := currentUserIDOr(r, 0)
		if m.h.mcp != nil {
			m.h.mcp.Set(uid, nil)
		}
		writeJSON(w, http.StatusOK, mcpStatusResponse{Connected: false})

	default:
		writeError(w, http.StatusBadRequest, "action must be 'connect' or 'disconnect'")
	}
}

// managerBlockList returns the block-list pattern string from the shared config so
// a /mcp connect honours MCP_BLOCK_LIST just like NewManager does. It returns an
// empty string when the config is unavailable.
func (m *mcpHandler) managerBlockList() string {
	if m.h.Cfg == nil {
		return ""
	}
	return strings.TrimSpace(m.h.Cfg.MCPBlockList)
}

// toolsFromManager returns the current tool descriptors from the manager, empty
// when nil or disconnected.
func toolsFromManager(mgr *mcpclient.ResilientMCPClient) []mcpToolSummary {
	if mgr == nil {
		return nil
	}
	tools := mgr.Tools()
	out := make([]mcpToolSummary, 0, len(tools))
	for _, t := range tools {
		out = append(out, mcpToolSummary{Name: t.Name, Description: t.Description})
	}
	return out
}
