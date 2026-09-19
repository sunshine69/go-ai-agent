// Package tools provides the building blocks for model-driven tool use
// (function calling): listing the available MCP tools as OpenAI-compatible
// schemas, and executing a single tool call through the MCP client.
//
// This is a small, dependency-light layer that the tool-use controller
// (internal/tooluse) orchestrates. The models here (Provider) are intentionally
// focused so the controller stays readable.
package tools

import (
	"fmt"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/mcpclient"
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

// IsNil reports whether the underlying MCP client is nil (MCP disabled or
// not yet connected). Callers that only need to know "are there tools" can
// use HasTools(); this additionally covers a nil inner client.
func (p *Provider) IsNil() bool {
	return p.manager == nil
}

// CallTool invokes a named MCP tool through the underlying client. It is only
// called when the caller has already confirmed a client is present (via IsNil),
// but it is itself nil-safe.
func (p *Provider) CallTool(name string, args map[string]any) (string, error) {
	if p.manager == nil {
		return "", fmt.Errorf("no MCP server connected")
	}
	return p.manager.CallTool(name, args)
}
