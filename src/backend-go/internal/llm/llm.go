// Package llm wraps an OpenAI-compatible /v1/chat/completions endpoint,
// mirroring the Python backend's get_llm_answer() (which used litellm with the
// openai/<model> prefix). It supports a base_url (local llama.cpp / ollama /
// vLLM), an api_key, temperature, and history injection so the model retains
// cross-turn context. Both one-shot (Answer) and streamed (AnswerStream)
// modes are supported.
//
// Beyond simple chat, llm also exposes the two primitives the tool-use
// controller needs: Complete() for one-shot assistant turns (so the loop can
// detect tool_calls) and StreamMessages() to stream the final answer back.
//
// The request/response models are OpenAI-compatible: CompletionRequest gains a
// Tools / ToolChoice field so a model can be handed a callable tool schema,
// and ChatMessage gains ToolCalls / ToolCallID so a full tool-use turn-taking
// history can be replayed turn-by-turn.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// defaultLLMTimeout is the per-request HTTP timeout used by a Client when the
// caller does not specify one. It mirrors the reference app's 45m TIMEOUT
// default. A per-client timeout is used (rather than a shared http.Client) so a
// single long-lived client cannot be blocked indefinitely by one slow request.
const defaultLLMTimeout = 45 * time.Minute

// ToolCall mirrors a single OpenAI-style tool_call emitted by the model inside
// an assistant message. Arguments arrive as a JSON string.
type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function FunctionToolCall `json:"function"`
}

// FunctionToolCall is the function spec (name + JSON arguments) of one tool call.
type FunctionToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatMessage is one conversation turn sent to the model.
//
// A normal turn carries Role+Content. A tool-request turn (role "assistant")
// additionally carries ToolCalls; a tool-response turn (role "tool") carries
// ToolCallID plus the tool's Content. This mirrors the OpenAI messages shape so
// the same slice can express the full tool-use turn-taking history.
type ChatMessage struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	ToolCalls  []*ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

// ToolChoice instructs the model how to select a function to call. It
// serialises to the OpenAI structured form:
//
//	{"type":"function","function":{"name":"add_tool"}}  // pin a specific tool
//	{"type":"function","function":{"name":"auto"}}      // let the model choose
//
// NOTE on wire compatibility: llama.cpp and ollama only accept the legacy
// bare-string form ("auto", "required", or "function:NAME") and reject this
// object form with a warning ("type must be string, but is object"). Other
// OpenAI-compatible servers (notably the qwopus-mtp model) ignore the bare
// string and require this object form. The caller decides which form to emit by
// inspecting the resolved backend — see backendToolChoice / encodeBody.
type ToolChoice struct {
	Type     string             `json:"type"`
	Function ToolChoiceFunction `json:"function"`
}

// ToolChoiceFunction is the function selector inside a ToolChoice.
type ToolChoiceFunction struct {
	Name string `json:"name"`
}

// ToolChoiceAuto returns the OpenAI object form for "select whichever tool".
// The bare-string form ("auto") is intentionally NOT returned here: servers
// disagree on which form they accept, so callers must pick via backendToolChoice.
func ToolChoiceAuto() *ToolChoice {
	return &ToolChoice{Type: "function", Function: ToolChoiceFunction{Name: "auto"}}
}
func ToolChoicePinned(name string) *ToolChoice {
	return &ToolChoice{Type: "function", Function: ToolChoiceFunction{Name: name}}
}

// backendToolChoice converts a *ToolChoice to the JSON literal the given backend
// expects. For llama.cpp/ollama (which reject the object form) it emits the
// bare string: "auto", "required", or "function:<name>". For everything else it
// emits the OpenAI object form. Returns nil when there is no choice.
func backendToolChoice(backend string, tc *ToolChoice) json.RawMessage {
	if tc == nil {
		return nil
	}
	name := tc.Function.Name
	if name == "" {
		name = "auto"
	}
	switch backend {
	case "llama_cpp", "ollama":
		// Plain sentinel names pass through verbatim.
		if name == "auto" || name == "required" || name == "none" {
			return json.RawMessage(`"` + name + `"`)
		}
		// A plain tool name (or the bare "function" selector) is pinned as
		// "function:<name>". An already-prefixed value is used as-is.
		if name == "function" {
			name = "function:default"
		} else if strings.HasPrefix(name, "function:") {
			// Already in the wire form.
		} else {
			name = "function:" + name
		}
		return json.RawMessage(`"` + name + `"`)
	default:
		b, _ := json.Marshal(tc)
		return b
	}
}

// encodeBody marshals a CompletionRequest into request-body bytes, choosing the
// tool_choice representation that matches the target backend. Only tool_choice
// differs between backends; all other fields are serialized identically. Returns
// a non-nil error only when the non-tool_choice fields fail to marshal.
func encodeBody(backend string, req CompletionRequest) ([]byte, error) {
	// Re-serialize the body with a backend-aware tool_choice. We marshal into a
	// map to swap the field rather than editing fields in place, so the result
	// is exactly what this backend will receive.
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if b := obj["tool_choice"]; b != nil {
		// Re-parse as a *ToolChoice so we can re-emit the right wire form.
		var tc ToolChoice
		if json.Unmarshal(b, &tc) == nil && tc.Type != "" && tc.Function.Name != "" {
			if bce := backendToolChoice(backend, &tc); bce != nil {
				obj["tool_choice"] = bce
			}
		}
	}
	return json.Marshal(obj)
}

// Config holds the parameters needed to reach the chat endpoint.
type Config struct {
	BaseURL     string // host only, e.g. http://192.168.20.23/v1 (may be empty for cloud)
	APIKey      string
	Model       string
	Temperature float64
	// Backend overrides the wire-format family of the backend. Values:
	// "llama_cpp" / "ollama" (bare-string tool_choice), or "" to auto-detect
	// from URL/model. Detection prefers the model string, then the base URL.
	// Timeout is the per-request HTTP timeout for requests made by the client.
	// When zero, New() uses the 45m default (mirroring the reference app's
	// TIMEOUT default). Set it to override that default per client.
	Timeout time.Duration
	Backend string
}

// Client wraps a chat-endpoint configuration.
type Client struct {
	cfg     Config
	backend string // resolved backend family used for wire-format decisions
	http    *http.Client
}

// New constructs a Client, resolving the wire-format backend family from cfg.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o"
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "sk-placeholder"
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultLLMTimeout
	}
	return &Client{cfg: cfg, backend: detectBackend(cfg), http: &http.Client{Timeout: timeout}}
}

// Backend reports the resolved wire-format family ("llama_cpp", "ollama", or
// "" for the generic OpenAI object form). It is safe to call before any request.
func (c *Client) Backend() string { return c.backend }

// detectBackend resolves the wire-format family for a Config. An explicit cfg.
// Backend wins; otherwise it is sniffed from the model name, then the base URL.
// llama.cpp and ollama only accept a bare-string tool_choice ("auto"/"required"/
// "function:NAME") and reject the OpenAI object form, which the old ToolChoice
// comment got wrong (it had no MarshalJSON and always emitted the object).
func detectBackend(cfg Config) string {
	if cfg.Backend != "" {
		return strings.ToLower(strings.TrimSpace(cfg.Backend))
	}
	// Model string is the most reliable signal for a local server: ollama uses
	// the "ollama/<name>" prefix and llama.cpp models are frequently named with
	// "llama" / "llamacpp".
	model := strings.ToLower(cfg.Model)
	// NOTE: check ollama before llama — the string "ollama" contains the
	// substring "llama", so a naïve Contains(model, "llama") would misclassify
	// an ollama model as llama.cpp.
	if strings.Contains(model, "ollama") {
		return "ollama"
	}
	if strings.Contains(model, "llama") || strings.Contains(model, "llamacpp") {
		return "llama_cpp"
	}
	// Fall back to the base URL (covers setups where the server host or path
	// carries the "llama"/"ollama" token but the model name does not).
	u := strings.ToLower(cfg.BaseURL)
	if strings.Contains(u, "ollama") {
		return "ollama"
	}
	if strings.Contains(u, "llama") || strings.Contains(u, "llamacpp") {
		return "llama_cpp"
	}
	return "" // generic OpenAI object form
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.cfg.Model }

// SetHTTPClient overrides the HTTP client (used in tests).
func (c *Client) SetHTTPClient(h *http.Client) { c.http = h }

// Temperature returns the configured sampling temperature as a pointer, so it
// can be attached to a CompletionRequest the way Answer/AnswerStream do. It
// always returns a non-nil pointer: honoring an explicitly-configured 0 means
// we must not drop the field from the JSON body (which would let the server
// fall back to its own default instead of the configured value).
func (c *Client) Temperature() *float64 {
	t := c.cfg.Temperature
	return &t
}

// CompletionRequest is the body of a chat completion request. Set Stream to
// true to request OpenAI-format server-sent-event chunks. Tools and ToolChoice
// are optional and only sent when the caller wants the model to be able to call
// MCP tools (function calling).
type CompletionRequest struct {
	Model       string           `json:"model"`
	Messages    []ChatMessage    `json:"messages"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	Stream      *bool            `json:"stream,omitempty"`
	Tools       []map[string]any `json:"tools,omitempty"`
	ToolChoice  *ToolChoice      `json:"tool_choice,omitempty"`
}

// CompletionChoice is one completion choice in the response.
type CompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

// CompletionResponse is a /v1/chat/completions response body.
type CompletionResponse struct {
	ID      string             `json:"id,omitempty"`
	Choices []CompletionChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message,omitempty"`
		Type    string `json:"type,omitempty"`
	} `json:"error,omitempty"`
}

// CompletionChunk mirrors a single SSE chunk from a streaming
// /v1/chat/completions?stream=true response (OpenAI-compatible: llama.cpp /
// ollama / vLLM). It accepts both the incremental "delta" form and the
// full-message form so we stream whatever the server emits.
type CompletionChunk struct {
	ID      string         `json:"id,omitempty"`
	Choices []streamChoice `json:"choices"`
}

// streamChoice is one choice in a streaming chunk. Prefer the delta form
// (llama.cpp / OpenAI); fall back to the message form (some servers).
type streamChoice struct {
	Index        int         `json:"index,omitempty"`
	Delta        streamDelta `json:"delta,omitempty"`
	Message      ChatMessage `json:"message,omitempty"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

// streamDelta holds the incremental token for this chunk.
//
// OpenAI-compatible servers emit streaming tool calls inside the delta object
// (llama.cpp / ollama / most servers); the full-message form appears in
// streamChoice.Message. Reading both keeps us compatible with either wire style.
//
// The tool calls arrive as streamToolCallDelta fragments (not []ToolCall) so we
// can read each fragment's own "index" field — that index is the stable key
// that groups all fragments of a single call into one tool call across chunks.
type streamDelta struct {
	Role      string                 `json:"role,omitempty"`
	Content   string                 `json:"content,omitempty"`
	ToolCalls []*streamToolCallDelta `json:"tool_calls,omitempty"`
}

// streamToolCallDelta is one streamed tool-call fragment. Its "index" groups
// fragments that belong to the same model-issued tool call (which the server
// may split across many chunks before emitting the arguments).
type streamToolCallDelta struct {
	Index    int              `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function FunctionToolCall `json:"function"`
}

// streamContent returns the incremental text in this choice, preferring the
// delta form, then falling back to the full message form.
func (s streamChoice) streamContent() string {
	if s.Delta.Content != "" {
		return s.Delta.Content
	}
	return s.Message.Content
}

// Answer streams the chat completion and returns the assistant's text. It
// mirrors the Python contract: on any error it returns a friendly error string
// rather than panicking.
//
// context holds the gathered MCP + RAG knowledge-base text. When present it is
// injected as an explicit user message between the history and the question,
// exactly as the Python get_llm_answer does, so the model answers from the
// retrieved documents instead of falling back to generic knowledge.
func (c *Client) Answer(ctx context.Context, system string, history []ChatMessage, context, user string) string {
	msgs := []ChatMessage{{Role: "system", Content: system}}
	for _, h := range history {
		if h.Role != "user" && h.Role != "assistant" {
			continue
		}
		if h.Content == "" {
			continue
		}
		msgs = append(msgs, h)
	}
	// Mirror Python get_llm_answer: inject context as a distinct user message
	// so the model treats it as genuine knowledge-base content.
	if strings.TrimSpace(context) != "" {
		msgs = append(msgs, ChatMessage{
			Role: "user",
			Content: fmt.Sprintf(
				"Here is additional context from the knowledge base:\n\n%s\n\nPlease answer this question:\n\n%s",
				context, user),
		})
	} else {
		msgs = append(msgs, ChatMessage{Role: "user", Content: user})
	}

	temp := c.cfg.Temperature
	reqBody := CompletionRequest{
		Model:       c.cfg.Model,
		Messages:    msgs,
		Temperature: &temp,
	}

	// Backend-aware encoding: llama.cpp/ollama require a bare-string tool_choice
	// (an object form is silently dropped, breaking tool use), so encodeBody
	// emits the form that matches this client's resolved backend.
	data, err := encodeBody(c.backend, reqBody)
	if err != nil {
		return "Sorry, I encountered an error preparing the request."
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return fmt.Sprintf("Sorry, I encountered an error: %s", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	httpResp, err := c.http.Do(req)
	if err != nil {
		return fmt.Sprintf("Sorry, I encountered an error: %s", err.Error())
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Sprintf("Sorry, I encountered an error: %s", err.Error())
	}

	if httpResp.StatusCode != http.StatusOK {
		apiErr := struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}{}
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error.Message != "" {
			return fmt.Sprintf("Sorry, I encountered an error: %s", apiErr.Error.Message)
		}
		return fmt.Sprintf("Sorry, I encountered an error (status %d).", httpResp.StatusCode)
	}

	var resp CompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Sprintf("Sorry, I encountered an error parsing the response: %s", err.Error())
	}
	if resp.Error != nil {
		return fmt.Sprintf("Sorry, I encountered an error: %s", resp.Error.Message)
	}
	if len(resp.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}

// AnswerStream streams the chat completion from the OpenAI-compatible
// /v1/chat/completions?stream=true endpoint, calling sink(token) for each
// incremental text chunk as it arrives. On success it returns the
// fully-assembled answer (the concatenation of every streamed chunk). On error
// it returns non-nil and sink may or may not have been called with partial
// content; the caller decides how to surface it.
//
// The context-injection logic mirrors Answer: context is passed as a distinct
// user message between the history and the question.

// ToolCallMaps converts []*ToolCall into the []map[string]any shape the
// datastore persists in the tool_calls column (and the tool-use controller
// emits). It preserves order and every field the SPA and model rely on.
func ToolCallMaps(calls []*ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		out = append(out, map[string]any{
			"id":        c.ID,
			"type":      c.Type,
			"name":      c.Function.Name,
			"arguments": c.Function.Arguments,
		})
	}
	return out
}

func (c *Client) AnswerStream(ctx context.Context, system string, history []ChatMessage, context, user string, sink func(string)) error {

	msgs := []ChatMessage{{Role: "system", Content: system}}
	for _, h := range history {
		if h.Role != "user" && h.Role != "assistant" {
			continue
		}
		if h.Content == "" {
			continue
		}
		msgs = append(msgs, h)
	}
	if strings.TrimSpace(context) != "" {
		msgs = append(msgs, ChatMessage{
			Role: "user",
			Content: fmt.Sprintf(
				"Here is additional context from the knowledge base:\n\n%s\n\nPlease answer this question:\n\n%s",
				context, user),
		})
	} else {
		msgs = append(msgs, ChatMessage{Role: "user", Content: user})
	}

	temp := c.cfg.Temperature
	stream := true
	reqBody := CompletionRequest{
		Model:       c.cfg.Model,
		Messages:    msgs,
		Temperature: &temp,
		Stream:      &stream,
	}

	data, err := encodeBody(c.backend, reqBody)
	if err != nil {
		return fmt.Errorf("prepare request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions?stream=true", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	httpResp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("streaming request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return fmt.Errorf("streaming endpoint returned status %d: %s", httpResp.StatusCode, truncate(body, 500))
	}

	// Parse the SSE line stream. Each meaningful line is "data: {json}".
	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var full strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue // blank line or SSE comment
		}
		if !strings.HasPrefix(line, "data:") {
			continue // e.g. "id: ...", "event: ..."
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk CompletionChunk
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			delta := ch.streamContent()
			if delta == "" {
				continue
			}
			full.WriteString(delta)
			sink(delta)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}

// FromToolCalls converts []map[string]any (as persisted in the datastore's
// tool_calls column, matching the SPA's expected shape) back into the
// []*ToolCall shape the LLM request requires. It is the inverse of
// ToolCallMaps. Any entry missing a function name or arguments is skipped so a
// partial row cannot wedge a request.
func FromToolCalls(tcs []map[string]any) []*ToolCall {
	out := make([]*ToolCall, 0, len(tcs))
	for _, tc := range tcs {
		name, _ := tc["name"].(string)
		args, _ := tc["arguments"].(string)
		if name == "" {
			continue
		}
		id, _ := tc["id"].(string)
		typ, ok := tc["type"].(string)
		if !ok || typ == "" {
			typ = "function"
		}
		out = append(out, &ToolCall{
			ID:       id,
			Type:     typ,
			Function: FunctionToolCall{Name: name, Arguments: args},
		})
	}
	return out
}

// Complete performs a one-shot (non-streaming) chat completion over the given
// messages. It returns the assistant message (role, content, and any tool_calls
// the model emitted). On transport-level error it returns non-nil; callers must
// check err before reading the result. This is the method the tool-use loop
// calls each turn — one request per assistant turn until no tool_calls remain.
func (c *Client) Complete(ctx context.Context, reqBody CompletionRequest) (*CompletionResponse, error) {
	// Log the incoming completion request so we can see what is being
	// sent to the model, including whether tools are attached.
	log.Printf("[LLM] Complete: model=%s stream=false tools=%d backend=%s", reqBody.Model, len(reqBody.Tools), c.backend)
	data, err := encodeBody(c.backend, reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	httpResp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("completion returned status %d: %s", httpResp.StatusCode, truncate(body, 500))
	}

	var resp CompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("completion error: %s", resp.Error.Message)
	}
	return &resp, nil
}

// StreamMessages streams an arbitrary messages array (already built by the
// caller, including tools) to the OpenAI-compatible endpoint and returns the
// fully-assembled final text. It accepts both OpenAI (delta) and full-message
// streaming forms. Used to stream the tool-use controller's final answer
// token-by-token. On error it returns non-nil.
func (c *Client) StreamMessages(ctx context.Context, reqBody CompletionRequest, sink func(string)) error {
	data, err := encodeBody(c.backend, reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions?stream=true", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	httpResp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("streaming request failed: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return fmt.Errorf("streaming endpoint returned status %d: %s", httpResp.StatusCode, truncate(body, 500))
	}
	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk CompletionChunk
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			delta := ch.streamContent()
			if delta == "" {
				continue
			}
			sink(delta)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return nil
}

// streamTurnResult holds the outcome of a single streamed tool-use turn:
// the fully-assembled streamed content and any tool_calls the model
// requested that must be executed before the next turn.
type streamTurnResult struct {
	Content   string
	ToolCalls []*ToolCall
}

// StreamTurn performs a single streaming chat-completion turn with the same
// options as Complete(), but emits each streamed text chunk via sink (real
// time) AND accumulates the model's tool_calls across streaming chunks.
//
// This mirrors the reference app's streamOnce: tool_calls arrive in JSON
// delta fragments that may be split across several SSE chunks (the ID and
// function name often precede the arguments), so they must be accumulated
// by tool-call index before the loop can execute them. sink may be nil.
// On error it returns non-nil and sink may have received partial content.
func (c *Client) StreamTurn(ctx context.Context, reqBody CompletionRequest, sink func(string)) (*streamTurnResult, error) {
	// Mirror AnswerStream: some OpenAI-compatible servers require the
	// stream:true flag in the body in addition to the query param.
	stream := true
	reqBody.Stream = &stream
	data, err := encodeBody(c.backend, reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/chat/completions?stream=true", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	httpResp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("streaming request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("streaming endpoint returned status %d: %s", httpResp.StatusCode, truncate(body, 500))
	}

	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// Accumulate tool calls across streaming chunks. llama.cpp / ollama / most
	// OpenAI-compatible servers stream tool calls inside the *delta* object
	// (delta.tool_calls), and a single call is split across many chunks: its ID
	// and function name often arrive before its arguments. The OpenAI "index"
	// field on each tool_calls delta is the stable key that keeps all of a call's
	// fragments in one bucket, so we key by it (a running counter backs us up if
	// a server omits the index).
	type tcAccum struct {
		Index int
		ID    string
		Type  string
		Name  string
		Args  string
	}
	toolAccum := map[int]*tcAccum{}
	seenOrder := []int{} // keys in first-seen order, so results stay sorted
	var runningIdx int   // fallback position counter if a server omits "index"
	var msgIdx int       // counter for the message-form tool calls below
	var content strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk CompletionChunk
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			// finish_reason "tool_calls" means the model wants to call tools.
			// Keep scanning (don't break) so we collect every argument before
			// finalizing; a plain "stop" with a half-built call is handled by
			// the JSON-validation below.

			// Stream text content from the delta, if present.
			if ch.Delta.Content != "" {
				content.WriteString(ch.Delta.Content)
				if sink != nil {
					sink(ch.Delta.Content)
				}
			}

			// Read tool calls from the delta object — the form llama.cpp /
			// ollama / streaming servers actually use. This is the branch the
			// old code missed entirely, which is why every tool call vanished.
			sawDeltaCalls := false
			for _, tcDelta := range ch.Delta.ToolCalls {
				sawDeltaCalls = true
				if tcDelta == nil {
					continue
				}
				key := tcDelta.Index
				if key < 0 {
					key = runningIdx
					runningIdx++
				}
				if key < 0 {
					continue
				}
				entry := toolAccum[key]
				if entry == nil {
					entry = &tcAccum{Index: key}
					toolAccum[key] = entry
					seenOrder = append(seenOrder, key)
				}
				// Merge fragments WITHOUT clobbering a real field with a later
				// empty one.
				if tcDelta.ID != "" && entry.ID == "" {
					entry.ID = tcDelta.ID
				}
				if tcDelta.Type != "" && entry.Type == "" {
					entry.Type = tcDelta.Type
				}
				if tcDelta.Function.Name != "" && entry.Name == "" {
					entry.Name = tcDelta.Function.Name
				}
				entry.Args += tcDelta.Function.Arguments
			}

			// Fallback: some servers carry full messages (message.tool_calls)
			// instead of deltas; only use that form when a chunk carries no
			// delta tool calls, so we never duplicate one call twice.
			if !sawDeltaCalls {
				for _, tcMsg := range ch.Message.ToolCalls {
					if tcMsg == nil {
						continue
					}
					key := msgIdx
					msgIdx++
					entry := toolAccum[key]
					if entry == nil {
						entry = &tcAccum{Index: key}
						toolAccum[key] = entry
						seenOrder = append(seenOrder, key)
					}
					if tcMsg.ID != "" && entry.ID == "" {
						entry.ID = tcMsg.ID
					}
					if tcMsg.Type != "" && entry.Type == "" {
						entry.Type = tcMsg.Type
					}
					if tcMsg.Function.Name != "" && entry.Name == "" {
						entry.Name = tcMsg.Function.Name
					}
					entry.Args += tcMsg.Function.Arguments
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	// Finalize the accumulated tool calls, in call order, keeping only those
	// with a name and arguments that parse as valid JSON so the loop can safely
	// execute them next round.
	var toolCalls []*ToolCall
	for _, key := range seenOrder {
		e := toolAccum[key]
		if e == nil {
			continue
		}
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		if e.Type == "" {
			e.Type = "function"
		}
		args := strings.TrimSpace(e.Args)
		if args == "" {
			continue
		}
		var tmp any
		if json.Unmarshal([]byte(args), &tmp) != nil {
			log.Printf("[LLM] StreamTurn: discarding tool call %q with invalid arguments %q", name, args)
			continue
		}
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = fmt.Sprintf("call-%s", name)
		}
		toolCalls = append(toolCalls, &ToolCall{
			ID:   id,
			Type: e.Type,
			Function: FunctionToolCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	if len(toolCalls) > 0 {
		log.Printf("[LLM] StreamTurn: accumulated %d tool call(s)", len(toolCalls))
		for _, tc := range toolCalls {
			log.Printf("[LLM] StreamTurn: tool call %q args=%s", tc.Function.Name, tc.Function.Arguments)
		}
	}

	return &streamTurnResult{Content: content.String(), ToolCalls: toolCalls}, nil
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
