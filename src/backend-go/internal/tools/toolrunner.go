package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/mcpclient"
)

// Runner executes a single MCP tool call through the live manager and
// sanitises the result into a JSON string (or an "error: …" string) that the
// model can consume as a role "tool" content value.
type Runner struct {
	manager *mcpclient.ResilientMCPClient
}

// NewRunner builds a Runner from a live MCP manager. manager may be nil (MCP
// disabled); run() then returns a clear "unavailable" error so the model can
// recover rather than crash the loop.
func NewRunner(manager *mcpclient.ResilientMCPClient) *Runner {
	return &Runner{manager: manager}
}

// run executes toolName with the JSON-string arguments the model produced and
// returns its result text, or an "error: …" string on failure. The model sees
// this string as a tool turn and may refine args or call another tool.
func (r *Runner) run(ctx context.Context, toolName, argsJSON string) string {
	// Parse the arguments the model produced. MCP args arrive as a JSON string
	// (that is how OpenAI emits them). Default to {} on parse failure.
	args := map[string]any{}
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "error: failed to parse tool arguments: " + err.Error()
		}
	}
	if r.manager == nil {
		return "error: MCP tool " + toolName + " unavailable (MCP disabled)"
	}
	result, err := r.manager.CallTool(toolName, args)
	if err != nil {
		return "error: MCP tool " + toolName + " error: " + err.Error()
	}
	return result
}
