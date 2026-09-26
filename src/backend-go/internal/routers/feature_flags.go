// Package routers — feature_flags.go: decides whether a message turn is served
// by the model-driven tool-use controller (function calling) or by the classic
// hybrid (ContextBuilder + text-injection) path. The choice is gated by the
// FEATURE_TOOL_USE config knob and, in "auto", by a one-shot capability probe.
package routers

import (
	"context"
	"net/http"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/config"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/llm"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/tools"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/tooluse"
)

// newToolUseController builds a tool-use orchestrator from the handlers'
// dependencies. It is used by the message handlers when the FEATURE_TOOL_USE
// knob enables tool mode. A nil controller means "tool use unavailable" — the
// caller falls back to the hybrid ContextBuilder path.
//
// llmClient is the fully-resolved client for this request: it already carries
// the per-user /url base-URL override AND the per-user /m model override, so
// the controller talks to the correct host and model without the caller
// re-resolving either.
func newToolUseController(r *http.Request, h Handlers, llmClient *llm.Client) *tooluse.ToolUse {
	// When MCP is disabled the model has no tools to call, so there is no
	// point entering tool mode; the controller would loop with every tool
	// returning "unavailable".
	if h.mcpClient(r) == nil {
		return nil
	}
	provider := tools.NewProvider(h.mcpClient(r))
	maxCalls := h.Cfg.MODEL_MAX_TOOL_CALLS
	if maxCalls <= 0 {
		maxCalls = 5
	}
	return tooluse.New(llmClient, provider, maxCalls)
}

// shouldUseToolUse reports whether the tool-use controller should serve the
// current turn. Behaviour mirrors the plan's Phase A rollout:
//
//	"false" = never — always use the hybrid ContextBuilder path.
//	"true"  = always — hand tools to the model every turn.
//	"auto"  = probe-then-use — run a cheap capability probe with a single tool;
//	          switch to tool use only if the server actually emits tool_calls.
func shouldUseToolUse(ctx context.Context, cfg *config.Config, llmClient *llm.Client) bool {
	mode := cfg.FEATURE_TOOL_USE
	switch mode {
	case "":
		mode = "false" // safe default: keep the current baseline.
	}

	switch mode {
	case "true":
		return true
	case "false":
		return false
	default: // "auto" (or anything else) — probe once.
		return probeToolUse(ctx, llmClient)
	}
}

// probeToolUse sends a single tiny request with one tool attached to confirm the
// server supports function calling. It returns true only when the server
// responds with tool_calls (i.e. it can act on them). Probe failures fall back
// to hybrid mode so plain questions never break.
func probeToolUse(ctx context.Context, llmClient *llm.Client) bool {
	if llmClient == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Build a request that carries a single tool schema, and ask the model to
	// use it. A server that understands function calling will emit a tool_call
	// for the provided tool; a server that lacks support will return either a
	// plain answer or an error.
	toolSchema := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "probe_test_tool",
			"description": "A single probe tool used to confirm tool-call support.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}

	// Use the model name from the resolved client (which already carries the
	// per-user /m override) rather than cfg.LLMModel, so the probe targets the
	// same model the user selected.
	reqBody := llm.CompletionRequest{
		Model: llmClient.Model(),
		Messages: []llm.ChatMessage{
			{Role: "user", Content: "Use the available tool now."},
		},
		Tools: []map[string]any{toolSchema},
	}

	resp, err := llmClient.Complete(ctx, reqBody)
	if err != nil {
		return false
	}
	if resp == nil || len(resp.Choices) == 0 {
		return false
	}

	// Success path: check the emitted message for tool_calls. A real
	// function-calling server will ask to call the probe tool.
	choices := resp.Choices
	if len(choices) > 0 && len(choices[0].Message.ToolCalls) > 0 {
		return true
	}

	// Some servers answer without tool_calls even when they support calling.
	// Treat a valid, non-error response to a tools-carrying request as a signal
	// that the endpoint tolerates the richer request shape. The controller will
	// attach real tools on the actual turn and confirm via Available().
	//
	// However, if the model simply ignored the tools and gave a plain answer,
	// it is safer to stay in hybrid mode. Only return true when the model
	// actually exercised the tools.
	return false
}
