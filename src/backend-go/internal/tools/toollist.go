// Package tools provides the building blocks for model-driven tool use
// (function calling): listing the available MCP tools as OpenAI-compatible
// schemas, and executing a single tool call through the MCP client.
//
// This is a small, dependency-light layer that the tool-use controller
// (internal/tooluse) orchestrates. The models here (Provider) are intentionally
// focused so the controller stays readable.
package tools

import (
	"github.com/stevek/go-ai-agent/backend-go/internal/mcpclient"
)

// Provider lists the MCP tools available to the model as OpenAI-compatible
// tool schemas. It wraps the live MCP client (Manager) and converts its tool
// descriptors on demand via mcpclient.ToOpenAITools().
type Provider struct {
	manager *mcpclient.ResilientMCPClient
}

// NewProvider builds a tool schema provider from a live MCP manager. The
// manager may be nil when MCP is disabled, in which case ToOpenAITools() is
// never called and the controller falls back to plain text mode.
func NewProvider(manager *mcpclient.ResilientMCPClient) *Provider {
	return &Provider{manager: manager}
}

// Available returns the OpenAI-compatible tools array the model may call. When
// MCP is disabled (manager is nil) it returns nil so the caller omits the
// tools field from the request.
func (p *Provider) Available() []map[string]any {
	if p.manager == nil {
		return nil
	}
	return mcpclient.ToOpenAITools(p.manager.Tools())
}

// HasTools reports whether any MCP tools are available for the model to call.
func (p *Provider) HasTools() bool {
	if p.manager == nil {
		return false
	}
	return len(p.manager.Tools()) > 0
}

// Manager returns the underlying MCP manager, or nil when MCP is disabled.
func (p *Provider) Manager() *mcpclient.ResilientMCPClient {
	return p.manager
}
