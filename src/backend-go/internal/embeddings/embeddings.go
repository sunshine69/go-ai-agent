// Package embeddings provides a client for an OpenAI-compatible /v1/embeddings
// endpoint. The Python backend embeds locally with SentenceTransformer
// (all-MiniLM-L6-v2, 384-dim); this client lets a local OpenAI-compatible
// server (e.g. llama.cpp / ollama / vLLM) serve the same embeddings, so the Go
// backend is not tied to a local Python embedding process.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// defaultClient is the shared HTTP client for embedding requests.
var defaultClient = &http.Client{Timeout: 120 * time.Second}

// client is overridable in tests.
var client = defaultClient

// SetHTTPClient overrides the HTTP client used for requests (used in tests).
func SetHTTPClient(c *http.Client) { client = c }

// request models an OpenAI-compatible /v1/embeddings request body.
type request struct {
	Model          string      `json:"model"`
	Input          interface{} `json:"input"` // string or []string
	EncodingFormat string      `json:"encoding_format,omitempty"`
	Dimensions     *int        `json:"dimensions,omitempty"`
}

// embeddingResult is a single embedding returned by the API.
type embeddingResult struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
	Object    string    `json:"object"`
}

// response models the API response body.
type response struct {
	Data   []embeddingResult `json:"data"`
	Object string            `json:"object"`
	Model  string            `json:"model"`
	Usage  usage             `json:"usage,omitempty"`
}

type usage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// apiError represents an error response from the embeddings API.
type apiError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// Config holds the parameters needed to embed text.
type Config struct {
	BaseURL string // e.g. http://localhost:8080 (host only, no path)
	APIKey  string
	Model   string
}

// Client wraps an embeddings endpoint configuration.
type Client struct {
	cfg    Config
	dim    int
}

// New constructs an embeddings Client.
func New(cfg Config, dim int) *Client {
	return &Client{cfg: cfg, dim: dim}
}

// EmbedText embeds a single text string, returning the float32 vector.
func (c *Client) EmbedText(ctx context.Context, text string) ([]float32, error) {
	resp, err := c.embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("embeddings: empty response (no data entries)")
	}
	return resp.Data[0].Embedding, nil
}

// EmbedTexts embeds multiple text strings in one batched request, returning a
// slice of vectors aligned with the input order.
func (c *Client) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	resp, err := c.embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: expected %d results, got %d", len(texts), len(resp.Data))
	}
	out := make([][]float32, len(texts))
	for i, d := range resp.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// Embed returns the embedding for a single text string (Embedder interface).
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 1 {
		r, err := c.embed(ctx, texts[0])
		if err != nil {
			return nil, err
		}
		out := make([][]float32, len(r.Data))
		for i, d := range r.Data {
			out[i] = d.Embedding
		}
		return out, nil
	}
	resp, err := c.embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings: expected %d results, got %d", len(texts), len(resp.Data))
	}
	out := make([][]float32, len(texts))
	for i, d := range resp.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// Dimensions returns the embedding dimension.
func (c *Client) Dimensions() int { return c.dim }

func (c *Client) embed(ctx context.Context, input interface{}) (*response, error) {
	body, err := json.Marshal(request{
		Model:          c.cfg.Model,
		Input:          input,
		EncodingFormat: "float",
	})
	if err != nil {
		return nil, fmt.Errorf("embeddings: marshal request: %w", err)
	}

	url := c.cfg.BaseURL + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embeddings: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	httpResp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("embeddings: read body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		var apiErr apiError
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, fmt.Errorf("embeddings: API error (%s): %s", apiErr.Error.Type, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("embeddings: unexpected status %d: %s", httpResp.StatusCode, truncate(data, 500))
	}

	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("embeddings: decode response: %w", err)
	}
	return &resp, nil
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
