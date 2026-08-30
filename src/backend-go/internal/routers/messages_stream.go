package routers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	ctxpkg "github.com/stevek/go-ai-agent/backend-go/internal/context"
	"github.com/stevek/go-ai-agent/backend-go/internal/llm"
)

// messageStreamHandler exposes POST /api/messages/stream and proxyLLMStream, 
// endpoints that stream LLM answers to the client token-by-token.
type messageStreamHandler struct {
	h Handlers
}

func newMessagesStreamHandler(h Handlers) *messageStreamHandler {
	return &messageStreamHandler{h: h}
}

// Helper functions for SSE formatting
func formatSourcesForSSE(sources []string) string {
	data, _ := json.Marshal(sources)
	return string(data)
}

func formatCitationsForSSE(citations []confluenceLink) string {
	data, _ := json.Marshal(citations)
	return string(data)
}

func escapeSSE(text string) string {
	// Simple escaping for SSE - replace quotes and backslashes
	text = strings.ReplaceAll(text, "\\", "\\\\")
	text = strings.ReplaceAll(text, "\"", "\\\"")
	text = strings.ReplaceAll(text, "\n", "\\n")
	return text
}

func writeSSEError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// streamChatRequest represents the payload for streaming chat requests.
type streamChatRequest struct {
	Message        string  `json:"message"`
	ConversationID *string `json:"conversation_id,omitempty"`
	Domain         string  `json:"domain,omitempty"`
	SubCategory    string  `json:"sub_category,omitempty"`
}

// handleStreamChat implements POST /api/messages/stream with OpenAI-compatible streaming.
func (m *messageStreamHandler) handleStreamChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeSSEError(w, "method not allowed")
		return
	}

	var req streamChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSSEError(w, "invalid request body: "+err.Error())
		return
	}

	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeSSEError(w, "message is required")
		return
	}

	// Resolve conversation ID (create new if none provided)
	var convID string
	if req.ConversationID != nil && *req.ConversationID != "" {
		if existing := getConversation(*req.ConversationID); existing != nil {
			convID = existing.ID
		}
	}
	if convID == "" {
		newConv := createNewConversation()
		convID = newConv.ID
	}

	// Build context using ContextBuilder (MCP + RAG)
	builder := ctxpkg.New(m.h.Cfg, m.h.Manager, m.h.Rag)
	contextText, sources, confluenceRefs := builder.BuildContext(msg, req.Domain, req.SubCategory)

	confluenceBaseURL := strings.TrimSuffix(m.h.Cfg.ConfluenceBaseURL, "/")
	citations := []confluenceLink{}
	if confluenceBaseURL != "" {
		for _, ref := range confluenceRefs {
			citations = append(citations, confluenceLink{
				Title: ref["title"],
				URL:   confluenceBaseURL + "/pages/viewpage.action?pageId=" + ref["id"],
			})
		}
	}

	// Prepare system prompt
	sysPrompt := `You are a helpful AI assistant with TWO identities:

## Identity 1: GenIQ (knowledge mode)
You are an expert knowledge assistant for this organization.
You answer questions about the organization's procedures, forms, skills, processes, and policies.
You are thorough, accurate, and cite sources when referencing knowledge base content.

## Identity 2: Friendly Agent (casual mode)
You are a fun, casual AI assistant.
You answer general questions, tell jokes, chat, and be helpful in everyday ways.
You are witty, friendly, and approachable — like a helpful coworker who's also funny.`

	// Prepare final message with context injection
	var userMsg string
	if strings.TrimSpace(contextText) != "" {
		userMsg = fmt.Sprintf(
			"Here is additional context from the knowledge base:\n\n%s\n\nPlease answer this question:\n\n%s",
			contextText, msg)
	} else {
		userMsg = msg
	}

	// --- SSE Streaming Setup ---
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send conversation ID early so frontend can track session
	if _, err := w.Write([]byte(fmt.Sprintf(
		"event: context\nid: %d\ndata: {\"conversation_id\":\"%s\",\"sources\":%v}\n\n",
		time.Now().UnixNano(), convID, formatSourcesForSSE(sources)))); err != nil {
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Build messages array including history if conversation exists
	history := []llm.ChatMessage{}
	if convID != "" {
		if conv := getConversation(convID); conv != nil {
			for _, m := range conv.Messages {
				history = append(history, llm.ChatMessage{
					Role:    m.Role,
					Content: m.Content,
				})
			}
		}
	}

	msgs := []llm.ChatMessage{{Role: "system", Content: sysPrompt}}
	for _, h := range history {
		if (h.Role == "user" || h.Role == "assistant") && h.Content != "" {
			msgs = append(msgs, h)
		}
	}
	msgs = append(msgs, llm.ChatMessage{Role: "user", Content: userMsg})

	temp := m.h.Cfg.LLMTemperature
	stream := true
	reqBody := llm.CompletionRequest{
		Model:       m.h.Cfg.LLMModel,
		Messages:    msgs,
		Temperature: &temp,
		Stream:      &stream,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		writeSSEError(w, "failed to marshal request")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	llamaReq, err := http.NewRequestWithContext(ctx, "POST", 
		m.h.Cfg.LLMBASEURL+"/chat/completions?stream=true",
		bytes.NewBuffer(data))
	if err != nil {
		writeSSEError(w, "failed to create request: "+err.Error())
		return
	}

	llamaReq.Header.Set("Content-Type", "application/json")
	if m.h.Cfg.LLMAPIKey != "" && !strings.HasPrefix(m.h.Cfg.LLMBASEURL, "http://localhost") {
		llamaReq.Header.Set("Authorization", "Bearer "+m.h.Cfg.LLMAPIKey)
	}

	client := &http.Client{}
	llamaResp, err := client.Do(llamaReq)
	if err != nil {
		writeSSEError(w, "failed to connect to LLM server: "+err.Error())
		return
	}
	defer llamaResp.Body.Close()

	rc := http.NewResponseController(w)

	scanner := bufio.NewScanner(llamaResp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue // blank line or SSE comment
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		// Send token as SSE event
		eventData := fmt.Sprintf("{\"content\":\"%s\"}", escapeSSE(payload))
		if _, err := w.Write([]byte("event: message\ndata: " + eventData + "\n\n")); err != nil {
			return
		}
		
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		rc.Flush()
	}

	if scanner.Err() != nil {
		log.Printf("stream reader error: %v", scanner.Err())
		errorData := fmt.Sprintf("{\"error\":\"%s\"}", escapeSSE(scanner.Err().Error()))
		w.Write([]byte("event: error\ndata: " + errorData + "\n\n"))
		
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Send completion event with full response and sources
	responseData := fmt.Sprintf(
		"\"conversation_id\":\"%s\",\"sources\":%v,\"citations\":%v",
		convID, formatSourcesForSSE(sources), formatCitationsForSSE(citations))
	
	w.Write([]byte("event: done\ndata: {" + responseData + "}\n\n"))

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// proxyLLMStream directly proxies the LLM server's stream to the client.
// It builds context first, then sends a streaming request to the AI server
// and relays the SSE chunks as-is (minimal transformation).
func (m *messageStreamHandler) proxyLLMStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeSSEError(w, "method not allowed")
		return
	}

	var req streamChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSSEError(w, "invalid request body: "+err.Error())
		return
	}

	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeSSEError(w, "message is required")
		return
	}

	// Resolve conversation ID (create new if none provided)
	var convID string
	if req.ConversationID != nil && *req.ConversationID != "" {
		if existing := getConversation(*req.ConversationID); existing != nil {
			convID = existing.ID
		}
	}
	if convID == "" {
		newConv := createNewConversation()
		convID = newConv.ID
	}

	// Build context using ContextBuilder (MCP + RAG)
	builder := ctxpkg.New(m.h.Cfg, m.h.Manager, m.h.Rag)
	contextText, sources, confluenceRefs := builder.BuildContext(msg, req.Domain, req.SubCategory)

	confluenceBaseURL := strings.TrimSuffix(m.h.Cfg.ConfluenceBaseURL, "/")
	citations := []confluenceLink{}
	if confluenceBaseURL != "" {
		for _, ref := range confluenceRefs {
			citations = append(citations, confluenceLink{
				Title: ref["title"],
				URL:   confluenceBaseURL + "/pages/viewpage.action?pageId=" + ref["id"],
			})
		}
	}

	// Prepare system prompt (same as in handleStreamChat)
	sysPrompt := `You are a helpful AI assistant with TWO identities:

## Identity 1: GenIQ (knowledge mode)
You are an expert knowledge assistant for this organization.
You answer questions about the organization's procedures, forms, skills, processes, and policies.
You are thorough, accurate, and cite sources when referencing knowledge base content.

## Identity 2: Friendly Agent (casual mode)
You are a fun, casual AI assistant.
You answer general questions, tell jokes, chat, and be helpful in everyday ways.
You are witty, friendly, and approachable — like a helpful coworker who's also funny.`

	// Prepare final message with context injection
	var userMsg string
	if strings.TrimSpace(contextText) != "" {
		userMsg = fmt.Sprintf(
			"Here is additional context from the knowledge base:\n\n%s\n\nPlease answer this question:\n\n%s",
			contextText, msg)
	} else {
		userMsg = msg
	}

	// Build messages array including history if conversation exists
	history := []llm.ChatMessage{}
	if convID != "" {
		if conv := getConversation(convID); conv != nil {
			for _, m := range conv.Messages {
				history = append(history, llm.ChatMessage{
					Role:    m.Role,
					Content: m.Content,
				})
			}
		}
	}

	msgs := []llm.ChatMessage{{Role: "system", Content: sysPrompt}}
	for _, h := range history {
		if (h.Role == "user" || h.Role == "assistant") && h.Content != "" {
			msgs = append(msgs, h)
		}
	}
	msgs = append(msgs, llm.ChatMessage{Role: "user", Content: userMsg})

	// Prepare the request to LLM server
	temp := m.h.Cfg.LLMTemperature
	stream := true
	reqBody := llm.CompletionRequest{
		Model:       m.h.Cfg.LLMModel,
		Messages:    msgs,
		Temperature: &temp,
		Stream:      &stream,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		writeSSEError(w, "failed to marshal request")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	llamaReq, err := http.NewRequestWithContext(ctx, "POST", 
		m.h.Cfg.LLMBASEURL+"/chat/completions?stream=true",
		bytes.NewBuffer(data))
	if err != nil {
		writeSSEError(w, "failed to create request: "+err.Error())
		return
	}

	llamaReq.Header.Set("Content-Type", "application/json")
	if m.h.Cfg.LLMAPIKey != "" && !strings.HasPrefix(m.h.Cfg.LLMBASEURL, "http://localhost") {
		llamaReq.Header.Set("Authorization", "Bearer "+m.h.Cfg.LLMAPIKey)
	}

	client := &http.Client{}
	llamaResp, err := client.Do(llamaReq)
	if err != nil {
		writeSSEError(w, "failed to connect to LLM server: "+err.Error())
		return
	}
	defer llamaResp.Body.Close()

	// Set headers for SSE stream back to frontend
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Conversation-Id", convID)
	w.Header().Set("Access-Control-Expose-Headers", "X-Conversation-Id")

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	rc := http.NewResponseController(w)

	scanner := bufio.NewScanner(llamaResp.Body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

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

		// Relay the chunk directly to client (minimal transformation)
		if _, err := w.Write([]byte("event: message\ndata: " + payload + "\n\n")); err != nil {
			return
		}
		
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		rc.Flush()
	}

	if scanner.Err() != nil {
		log.Printf("stream reader error: %v", scanner.Err())
		errorData := fmt.Sprintf("{\"error\":\"%s\"}", escapeSSE(scanner.Err().Error()))
		w.Write([]byte("event: error\ndata: " + errorData + "\n\n"))
		
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Send completion event with conversation ID and sources
	responseData := fmt.Sprintf(
		"\"conversation_id\":\"%s\",\"sources\": %v,\"citations\": %v",
		convID, formatSourcesForSSE(sources), formatCitationsForSSE(citations))
	
	w.Write([]byte("event: done\ndata: {" + responseData + "}\n\n"))
	
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
