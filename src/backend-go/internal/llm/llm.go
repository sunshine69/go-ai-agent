// Package llm wraps an OpenAI-compatible /v1/chat/completions endpoint,
// mirroring the Python backend's get_llm_answer() (which used litellm with the
// openai/<model> prefix). It supports a base_url (local llama.cpp / ollama /
// vLLM), an api_key, temperature, and history injection so the model retains
// cross-turn context.
package llm

import (
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

// CompletionRequest is the body of a chat completion request.
type CompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
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
