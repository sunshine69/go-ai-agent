// Package tooluse implements model-driven tool use (function calling). It
// wraps the model so the decision of WHICH tools to call is made entirely by
// the LLM, passing the OpenAI-compatible tool schemas into each request and
// feeding tool results back as assistant/tool conversation turns.
//
// The flow mirrors a typical agent loop:
//
//  1. Send messages + tool schemas to the model (Complete).
//  2. If the model emits tool_calls, execute each through the MCP client and
//     append tool-request (assistant) + tool-response (tool) turns.
//  3. Repeat until the model returns a message with no tool_calls.
//  4. Stream the final message back to the client.
//
// This keeps the server from pre-fetching anything; it only mediates between
// the model and the tools it is allowed to call.
package tooluse

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/stevek/go-ai-agent/backend-go/internal/llm"
	"github.com/stevek/go-ai-agent/backend-go/internal/tools"
)

// ToolUse is the orchestrator that lets the model call MCP tools.
type ToolUse struct {
	llmClient *llm.Client
	provider  *tools.Provider
	maxCalls  int
}

// New creates a ToolUse orchestrator. provider may be nil when MCP is disabled,
// in which case the controller falls back to a single LLM call with no tools.
// maxCalls caps the number of tool-call rounds before the loop aborts.
func New(client *llm.Client, provider *tools.Provider, maxCalls int) *ToolUse {
	if maxCalls <= 0 {
		maxCalls = 5
	}
	return &ToolUse{llmClient: client, provider: provider, maxCalls: maxCalls}
}

// RunResult carries everything a caller needs to persist a single tool-use turn.
//
//   - FinalAnswer is the assembled text the model returned once it stopped
//     calling tools (also streamed via sink). Empty on error.
//   - Err is a human-readable problem string (or "" on success). When non-empty
//     the stream should still surface it to the user.
//   - PersistMsgs is the ordered list of messages created during the loop that
//     must be written to the datastore for round-trip multi-turn fidelity: an
//     "assistant" turn for each tool-request (carrying ToolCalls) followed by a
//     "tool" turn for each executed result (carrying ToolCallID + Content).
//     The final assistant answer turn is persisted separately by the caller via
//     persistMessage; here we only carry the tool-related turns.
type RunResult struct {
	FinalAnswer string
	Err         string
	PersistMsgs []*llm.ChatMessage
}

// Run executes the full tool-use loop over the given messages and streams the
// final answer token-by-token through sink. It returns a RunResult describing
// the streamed answer, any error, and the tool-related turns to persist.
//
// Each turn is streamed via llm.Client.StreamTurn so the client receives
// tokens in real time; the same single HTTP request also accumulates the
// model's tool_calls across streaming chunks, so a turn both streams its
// answer and reports whether it wants to call tools. The loop appends
// assistant/tool turns as it goes so each subsequent request sees the prior
// exchanges.
func (t *ToolUse) Run(ctx context.Context, messages []llm.ChatMessage, sink func(string)) (RunResult, error) {
	var finalAnswer strings.Builder
	var persistMsgs []*llm.ChatMessage

	// Determine whether to use tool mode for this request.
	useTools := t.provider != nil && t.provider.HasTools()

	for turn := 0; turn < t.maxCalls; turn++ {
		reqBody := llm.CompletionRequest{
			Model:    t.llmClient.Model(),
			// Surface the configured sampling temperature (mirrors
			// Answer/AnswerStream). Without this a nil Temperature is omitted
			// from the JSON body and the server falls back to its own default
			// (often 0 -> terse, no personality).
			Temperature: t.llmClient.Temperature(),
			Messages: append([]llm.ChatMessage{}, messages...),
		}
		if useTools {
			reqBody.Tools = t.provider.Available()
			// Instruct the model to select whichever tool it needs (the
			// structured OpenAI form). The bare-string "auto" is not
			// honoured by some OpenAI-compatible servers (e.g. qwopus-mtp),
			// which would otherwise silently ignore the tools.
			reqBody.ToolChoice = llm.ToolChoiceAuto()
		}

		// Log that the question reached the model. If in tool mode, also log
		// that the callable tool schemas are being handed to it so the operator
		// can confirm the model has tools available this turn.
		if useTools {
			log.Printf("[TOOL-USE] turn %d: sending question to model WITH tool schemas", turn)
		} else {
			log.Printf("[TOOL-USE] turn %d: sending question to model with no tools", turn)
		}

		// Log the stream result before continuing so we never show only a
		// "sending question" line with no outcome (fixes silent failures).
		res, err := t.llmClient.StreamTurn(ctx, reqBody, sink)
		if err != nil {
			log.Printf("[TOOL-USE] turn %d: model response error: %v", turn, err)
			return RunResult{Err: fmt.Sprintf("%s", err.Error()), FinalAnswer: finalAnswer.String()},
				fmt.Errorf("error talking to model: %s", err.Error())
		}

		// Log the outcome of the turn: either the model asked for tools or it
		// produced the final streamed answer (possibly empty if choked).
		if len(res.ToolCalls) > 0 {
			log.Printf("[TOOL-USE] turn %d: model requested %d tool call(s); streamed %d chars",
				turn, len(res.ToolCalls), len(res.Content))
		} else {
			log.Printf("[TOOL-USE] turn %d: model returned final answer (%d chars)",
				turn, len(res.Content))
		}
		// If the model wants to call tools, execute them and loop.
		if len(res.ToolCalls) > 0 {
			// Log which tools the model selected this turn, and their parsed
			// arguments, so the operator can see what the model tried to call.
			toolNames := make([]string, 0, len(res.ToolCalls))
			for _, tc := range res.ToolCalls {
				toolNames = append(toolNames, tc.Function.Name)
			}
			log.Printf("[TOOL-USE] turn %d: model selected tools: %v", turn, toolNames)

			// Record and append the assistant turn that requested tools.
			assistantTurn := &llm.ChatMessage{
				Role:      "assistant",
				Content:   res.Content,
				ToolCalls: res.ToolCalls,
			}
			persistMsgs = append(persistMsgs, assistantTurn)
			messages = append(messages, *assistantTurn)

			// Execute each tool call and append a tool-response turn.
			for _, tc := range res.ToolCalls {
				resultText := t.executeTool(ctx, tc.Function.Name, tc.Function.Arguments)
				persistMsgs = append(persistMsgs, &llm.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    resultText,
				})
				messages = append(messages, llm.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    resultText,
				})
			}
			continue
		}

		// No tool calls — this is the final answer.
		finalAnswer.WriteString(res.Content)
		return RunResult{FinalAnswer: finalAnswer.String(), PersistMsgs: persistMsgs}, nil
	}

	return RunResult{FinalAnswer: finalAnswer.String()}, fmt.Errorf("tool use hit the safety limit (%d turns)", t.maxCalls)
}

// executeTool runs a single MCP tool call and returns its result text, or an
// error message on failure. It re-uses the ResilientMCPClient via the tool
// provider.
func (t *ToolUse) executeTool(ctx context.Context, name, argsJSON string) string {
	// Parse the JSON arguments the model produced.
	args := map[string]any{}
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Failed to parse arguments for %s: %s", name, err.Error())
		}
	}

	if t.provider == nil || t.provider.IsNil() {
		return fmt.Sprintf("MCP tool %q unavailable", name)
	}

	result, err := t.provider.CallTool(name, args)
	if err != nil {
		return fmt.Sprintf("MCP tool %q error: %s", name, err.Error())
	}
	log.Printf("[TOOL-USE] executing tool %q args=%v", name, args)
	return result
}
