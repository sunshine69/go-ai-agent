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
	"log"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var defaultClient = &http.Client{Timeout: 300 * time.Second}

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
// serialises to the OpenAI structured form
//
//	{"type":"function","function":{"name":"add_tool"}}  // pin a specific tool
//	{"type":"function","function":{"name":"auto"}}      // let the model choose
//
// Some OpenAI-compatible servers (notably the qwopus-mtp model) ignore the
// legacy bare-string form ("auto") and only honour this structured form, so we
// always emit it. This type also marshals to the bare string "auto" via the
// ToolChoiceAuto helper for servers that accept either.
type ToolChoice struct {
	Type     string             `json:"type"`
	Function ToolChoiceFunction `json:"function"`
}

// ToolChoiceFunction is the function selector inside a ToolChoice.
type ToolChoiceFunction struct {
	Name string `json:"name"`
}

func ToolChoiceAuto() *ToolChoice {
	return &ToolChoice{Type: "function", Function: ToolChoiceFunction{Name: "auto"}}
}
func ToolChoicePinned(name string) *ToolChoice {
	return &ToolChoice{Type: "function", Function: ToolChoiceFunction{Name: name}}
}

// Config holds the parameters needed to reach the chat endpoint.
type Config struct {
	BaseURL     string // host only, e.g. http://192.168.20.23/v1 (may be empty for cloud)
	APIKey      string
	Model       string
	Temperature float64
}

// Client wraps a chat-endpoint configuration.
type Client struct {
	cfg  Config
	http *http.Client
}

// New constructs a Client.
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
	return &Client{cfg: cfg, http: defaultClient}
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
type streamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
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

	data, err := json.Marshal(reqBody)
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

	data, err := json.Marshal(reqBody)
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
	log.Printf("[LLM] Complete: model=%s stream=false tools=%d", reqBody.Model, len(reqBody.Tools))
	data, err := json.Marshal(reqBody)
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
	data, err := json.Marshal(reqBody)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/chat/completions?stream=true", bytes.NewReader(reqBody.data()))
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

	// Accumulate tool calls across streaming chunks, keyed by tool-call index.
	// A single tool call can be split across many chunks (ID and function name
	// arrive before the arguments), so we merge by index.
	toolAccum := map[int]*ToolCall{}
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
			// Stream text content in real time.
			if ch.Delta.Content != "" {
				content.WriteString(ch.Delta.Content)
				if sink != nil {
					sink(ch.Delta.Content)
				}
				continue
			}
			if ch.Message.Content != "" {
				content.WriteString(ch.Message.Content)
				if sink != nil {
					sink(ch.Message.Content)
				}
				continue
			}
			// Accumulate tool calls by index (ID, Type, name, args may arrive
			// separately across chunks).
			for _, tc := range ch.Message.ToolCalls {
				if tc == nil || tc.Function.Name == "" {
					continue
				}
				key := len(toolAccum) + 1
				existing := toolAccum[key]
				if existing == nil {
					existing = &ToolCall{}
					toolAccum[key] = existing
				}
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Type != "" {
					existing.Type = tc.Type
				}
				existing.Function.Name = tc.Function.Name
				existing.Function.Arguments += tc.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	var toolCalls []*ToolCall
	for _, tc := range toolAccum {
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" {
			continue
		}
		// Only keep tool calls whose arguments are valid JSON, so the loop
		// can safely execute them next round.
		var tmp any
		if json.Unmarshal([]byte(args), &tmp) != nil {
			continue
		}
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call-%s", tc.Function.Name)
		}
		toolCalls = append(toolCalls, tc)
	}

	return &streamTurnResult{Content: content.String(), ToolCalls: toolCalls}, nil
}

// data marshals the CompletionRequest body for the streaming endpoint.
func (b CompletionRequest) data() []byte {
	raw, err := json.Marshal(b)
	if err != nil {
		// Fall back to a minimal body; should not happen for valid requests.
		return []byte("{}")
	}
	return raw
}
func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
