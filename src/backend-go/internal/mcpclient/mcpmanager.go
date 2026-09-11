package mcpclient

import (
	"strings"

	"github.com/stevek/go-ai-agent/backend-go/internal/db"
	"sync"
)

// MCPManager resolves which ResilientMCPClient serves a given user, plus a
// shared default used for the anonymous "user 0" session.
//
// Every backend handler keeps a *copy* of the Handlers struct (which embeds a
// *MCPManager), so reading the manager per request is safe to do concurrently.
// This keeps a per-user connection in sync with everyone reading the same
// manager, while still honouring per-account preferences.
//
// Users who run `/mcp off` are tracked explicitly (see Set): a disconnected
// user no longer falls back to the shared default even though that default lives
// for the lifetime of the process. Removing only the override was not enough —
// Client() fell back to the process-wide default and would keep serving tools
// after a `/mcp off`.
//
// All methods are safe for concurrent use.
type MCPManager struct {
	defaultClient *ResilientMCPClient
	overrides     map[int64]*ResilientMCPClient
	disabled      map[int64]struct{}
	mu            sync.RWMutex
}

// NewMCPManager builds a manager seeded with the given shared default client.
// A nil default simply yields "no MCP server connected" everywhere.
func NewMCPManager(client *ResilientMCPClient) *MCPManager {
	return &MCPManager{
		defaultClient: client,
		overrides:     make(map[int64]*ResilientMCPClient),
		disabled:      make(map[int64]struct{}),
	}
}

// Client returns the client to serve uid. An explicitly disconnected user
// always receives nil, even while a shared default exists, so a `/mcp off`
// persists until the user reconnects. Otherwise the user-specific override is
// preferred, and any remaining callers fall back to the shared default. A nil
// return means "no MCP server connected" — callers must nil-guard their reads.
func (m *MCPManager) Client(uid int64) *ResilientMCPClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, off := m.disabled[uid]; off {
		return nil
	}
	if c, ok := m.overrides[uid]; ok {
		return c
	}
	return m.defaultClient
}

// Default returns the shared default client (user 0), or nil when none.
func (m *MCPManager) Default() *ResilientMCPClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultClient
}

// Set stores (or clears, when client is nil) the client serving uid. Any
// previously-assigned client for that user is closed in place before the new
// one is stashed, so a stdio child process never leaks.
//
// When client is nil the user is also flagged as disconnected (clearing any
// prior flag): removing the override alone was not enough, because Client()
// fell back to the process-wide default and would keep serving tools after a
// `/mcp off`. A non-nil client clears the flag, so a reconnect restores service
// even for a user who had previously disconnected.
func (m *MCPManager) Set(uid int64, client *ResilientMCPClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if client == nil {
		if prev, ok := m.overrides[uid]; ok {
			prev.Close()
			delete(m.overrides, uid)
		}
		m.disabled[uid] = struct{}{}
		return
	}
	delete(m.disabled, uid)
	if prev, ok := m.overrides[uid]; ok && prev != client {
		prev.Close()
	}
	m.overrides[uid] = client
}

// SwapDefault replaces the shared default client in place, closing the old
// default before stashing the new one (mirrors ResilientMCPClient.Swap so no
// stdio child process leaks). A nil client removes the default entirely.
func (m *MCPManager) SwapDefault(other *ResilientMCPClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev := m.defaultClient; prev != nil {
		prev.Close()
	}
	m.defaultClient = other
}

// DefaultMCPWorkdir reads the per-user "mcpWorkdir" setting for uid and returns
// it when the user has set a valid, usable one. The value is the raw (relative,
// ".."-free) selector the frontend stores — e.g. "mcp_data" — not the resolved
// absolute path: the caller (main.go) passes it straight into
// MCPManagerConfig.MCPWorkDir so the default (user-0) server inherits it, and the
// existing resolveConnectWorkdir path re-validates and resolves it.
//
// It returns ("", false) when the setting is absent, empty, invalid, or when the
// DB/settings are nil — in which case the caller keeps its configured value.
func DefaultMCPWorkdir(dbStore *db.DB, uid int64) (string, bool) {
	if dbStore == nil || dbStore.Settings == nil {
		return "", false
	}
	raw, err := dbStore.Settings.Get(uid, "mcpWorkdir")
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	if !IsValidWorkdir(trimmed) {
		return "", false
	}
	return trimmed, true
}
