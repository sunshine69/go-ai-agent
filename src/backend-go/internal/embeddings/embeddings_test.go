package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient spins up a mock OpenAI-compatible embeddings server and returns
// a Client pointed at it.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handler))
	return New(Config{BaseURL: srv.URL, APIKey: "test", Model: "test-model"}, 384), srv
}

func TestEmbedText(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify path and method.
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("bad method: %s", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test" {
			t.Errorf("bad auth header: %q", auth)
		}

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("bad model: %q", req.Model)
		}

		json.NewEncoder(w).Encode(response{
			Object: "list",
			Model:  req.Model,
			Data: []embeddingResult{
				{Embedding: []float32{0.1, 0.2, 0.3, 0.4}, Index: 0, Object: "embedding"},
			},
		})
	})
	defer srv.Close()

	vec, err := c.EmbedText(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("EmbedText: %v", err)
	}
	if len(vec) != 4 {
		t.Fatalf("expected 4 dims, got %d", len(vec))
	}
	if vec[3] != 0.4 {
		t.Fatalf("unexpected value %v", vec[3])
	}
}

func TestEmbedTexts(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Return two embeddings matching the two inputs.
		json.NewEncoder(w).Encode(response{
			Object: "list",
			Data: []embeddingResult{
				{Embedding: []float32{1, 0}, Index: 0, Object: "embedding"},
				{Embedding: []float32{0, 1}, Index: 1, Object: "embedding"},
			},
		})
	})
	defer srv.Close()

	vecs, err := c.EmbedTexts(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedTexts: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if vecs[0][0] != 1 || vecs[1][1] != 1 {
		t.Fatalf("unexpected vectors: %+v", vecs)
	}
}

func TestEmbedTextError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(apiError{
			Error: struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			}{
				Message: "invalid input",
				Type:    "invalid_request_error",
			},
		})
	})
	defer srv.Close()

	_, err := c.EmbedText(context.Background(), "hello")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestEmbedTextsCountMismatch(t *testing.T) {
	// Request 2 inputs but API returns 1 embedding -> should error on count.
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(response{
			Object: "list",
			Data: []embeddingResult{
				{Embedding: []float32{1, 2}, Index: 0, Object: "embedding"},
			},
		})
	})
	defer srv.Close()

	_, err := c.EmbedTexts(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatalf("expected count mismatch error, got nil")
	}
}
