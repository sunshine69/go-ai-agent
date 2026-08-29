// Package llm wraps an OpenAI-compatible /v1/chat/completions endpoint,
// mirroring the Python backend's get_llm_answer() (which used litellm with the
// openai/<model> prefix). It supports a base_url (local llama.cpp / ollama /
// vLLM), an api_key, temperature, and history injection so the model retains
// cross-turn context. Both one-shot (Answer) and streamed (AnswerStream)
// modes are supported.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var defaultClient = &http.Client{Timeout: 300 * time.Second}

// ChatMessage is one conversation turn sent to the model.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

// SetHTTPClient overrides the HTTP client (used in tests).
func (c *Client) SetHTTPClient(h *http.Client) { c.http = h }

// CompletionRequest is the body of a chat completion request. Set Stream to
// true to request OpenAI-format server-sent-event chunks.
type CompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Stream      *bool         `json:"stream,omitempty"`
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

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
