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

	ctxpkg "github.com/sunshine69/go-ai-agent/backend-go/internal/context"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/contextcompress"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/llm"
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

// persistStreamUserTurn persists the user turn that started the current
// streaming response. Tagging it with the current-turn key mirrors the non
// streaming path (messages.go) so the next turn's history replay excludes it.
func persistStreamUserTurn(m *messageStreamHandler, r *http.Request, conv db.ConvView, msg string) {
	if msg == "" || conv.ID == "" {
		return
	}
	// Persist to the ALREADY-resolved conversation (not a fresh one). Passing
	// an empty id to resolveConversation would create a second, orphaned
	// conversation — that is exactly what caused the "two conversations" bug.
	// Use the conv handed to us by the caller so the user turn lands in the
	// same conversation as the streamed assistant answer and its title.
	userKey := "__current_user__:" + msg
	persistMessage(m.h, r, conv, "user", msg, userKey, nil, nil, "")
}

// persistStreamResponse persists the assistant reply for the current streaming
// response. It extracts the text the same way the SPA (useStreaming) does from
// an OpenAI-style message event so the stored content matches what the client
// displays.
func persistStreamResponse(m *messageStreamHandler, r *http.Request, conv db.ConvView, content string) {
	if conv.ID == "" || content == "" {
		return
	}
	persistMessage(m.h, r, conv, "assistant", content, "", nil, nil, "")
}

// clientExtractedContent pulls the text chunk out of an incoming "message"
// event payload using the same precedence the SPA (useStreaming) applies: the
// {content: "..."} form (from /api/chat/stream) takes priority over the
// OpenAI-style {choices:[{delta:{content}}]} form.
func clientExtractedContent(payload string) string {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return ""
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		// Not JSON: this is a plain-text token payload; return it as-is.
		return trimmed
	}

	if raw, ok := obj["content"]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil && s != "" {
			return s
		}
	}

	if choices, ok := obj["choices"]; ok {
		var arr []map[string]json.RawMessage
		if err := json.Unmarshal(choices, &arr); err == nil && len(arr) > 0 {
			if delta, ok := arr[0]["delta"]; ok {
				var d map[string]json.RawMessage
				if err := json.Unmarshal(delta, &d); err == nil {
					if raw, ok := d["content"]; ok {
						var s string
						if err := json.Unmarshal(raw, &s); err == nil && s != "" {
							return s
						}
					}
				}
			}
		}
	}

	return ""
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

	// Resolve conversation (owned by the caller; creates a new one if the
	// requested id is missing or not owned).
	conv := resolveConversation(m.h, r, derefStr(req.ConversationID))
	if conv.ID == "" {
		writeSSEError(w, "failed to resolve conversation")
		return
	}
	convID := conv.ID

	// Persist the user turn that started this streaming response.
	persistStreamUserTurn(m, r, conv, msg)

	// Optional model-driven tool-use path: if enabled and the server can call
	// tools, serve this turn through the tool-use controller instead of the
	// hybrid ContextBuilder+text-injection path. If handled (or an error
	// occurred), stop here — otherwise fall through to the hybrid path.
	if handled, runErr := m.runToolUse(w, r, conv, msg, req.Domain, req.SubCategory); handled || runErr != nil {
		return
	}
	// Build context using ContextBuilder (MCP + RAG)
	builder := ctxpkg.New(m.h.Cfg, m.h.mcpClient(r), m.h.ragManager.Client(currentUserIDOr(r, 0)))
	contextText, sources, confluenceRefs := builder.BuildContext(msg, req.Domain, req.SubCategory)

	confluenceBaseURL := strings.TrimSuffix(m.h.Cfg.ConfluenceBaseURL, "/")
	citations := []confluenceLink{}
	if confluenceBaseURL != "" {
		for _, ref := range confluenceRefs {
			citations = append(citations, confluenceLink{
				Title: ref["title"],
				URL:   confluenceBaseURL + "/pages/viewpage?pageId=" + ref["id"],
			})
		}
	}

	// Prepare system prompt
	sysPrompt := resolveSystemPrompt(m.h.DB, currentUserIDOr(r, 0))
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

	// Build messages array including history from the resolved conversation
	history := []db.DBMessage{}
	for _, m := range conv.Messages {
		history = append(history, m)
	}

	// Compress the history if it exceeds the token budget. The streaming
	// handler works in llm.ChatMessage form, so we build a []db.DBMessage
	// first (so contextcompress.TrimContext can estimate and summarise
	// tokens), then re-map. No-op when compression is disabled
	// (cfg.ContextLimit == 0) or the burst limit is not reached.
	// Apply the caller's per-user context budget (via /ctx) before trimming.
	cfg := m.h.applyUserCtxLimit(m.h.Cfg, r)
	history = contextcompress.TrimContext(r.Context(), cfg, history)
	historyCM := make([]llm.ChatMessage, 0, len(history))
	for _, h := range history {
		historyCM = append(historyCM, llm.ChatMessage{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	msgs := []llm.ChatMessage{{Role: "system", Content: sysPrompt}}
	for _, h := range historyCM {
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
	log.Printf("[LLM] hybrid stream request: model=%s messages=%d stream=true", m.h.Cfg.LLMModel, len(msgs))

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
	var accContent string
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

		// Accumulate the streamed content for persistence
		if chunk := clientExtractedContent(payload); chunk != "" {
			accContent += chunk
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

	persistStreamResponse(m, r, conv, accContent)
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

	// Resolve conversation (owned by the caller; creates a new one if the
	// requested id is missing or not owned).
	conv := resolveConversation(m.h, r, derefStr(req.ConversationID))
	if conv.ID == "" {
		writeSSEError(w, "failed to resolve conversation")
		return
	}
	convID := conv.ID

	// Persist the user turn that started this streaming response.
	persistStreamUserTurn(m, r, conv, msg)

	// Optional model-driven tool-use path: if enabled and the server can call
	// tools, serve this turn through the tool-use controller instead of the
	// hybrid ContextBuilder+text-injection path. If handled (or an error
	// occurred), stop here — otherwise fall through to the hybrid path.
	if handled, runErr := m.runToolUse(w, r, conv, msg, req.Domain, req.SubCategory); handled || runErr != nil {
		return
	}
	// Build context using ContextBuilder (MCP + RAG)
	builder := ctxpkg.New(m.h.Cfg, m.h.mcpClient(r), m.h.ragManager.Client(currentUserIDOr(r, 0)))
	contextText, sources, confluenceRefs := builder.BuildContext(msg, req.Domain, req.SubCategory)

	confluenceBaseURL := strings.TrimSuffix(m.h.Cfg.ConfluenceBaseURL, "/")
	citations := []confluenceLink{}
	if confluenceBaseURL != "" {
		for _, ref := range confluenceRefs {
			citations = append(citations, confluenceLink{
				Title: ref["title"],
				URL:   confluenceBaseURL + "/pages/viewpage?pageId=" + ref["id"],
			})
		}
	}

	// Prepare system prompt (same as in handleStreamChat)
	sysPrompt := resolveSystemPrompt(m.h.DB, currentUserIDOr(r, 0))

	// Prepare final message with context injection
	var userMsg string
	if strings.TrimSpace(contextText) != "" {
		userMsg = fmt.Sprintf(
			"Here is additional context from the knowledge base:\n\n%s\n\nPlease answer this question:\n\n%s",
			contextText, msg)
	} else {
		userMsg = msg
	}

	// Build messages array including history from the resolved conversation
	history := []db.DBMessage{}
	for _, m := range conv.Messages {
		history = append(history, m)
	}

	// Compress the history if it exceeds the token budget. The streaming
	// handler works in llm.ChatMessage form, so we build a []db.DBMessage
	// first (so contextcompress.TrimContext can estimate and summarise
	// tokens), then re-map. No-op when compression is disabled
	// (cfg.ContextLimit == 0) or the burst limit is not reached.
	// Apply the caller's per-user context budget (via /ctx) before trimming.
	cfg := m.h.applyUserCtxLimit(m.h.Cfg, r)
	history = contextcompress.TrimContext(r.Context(), cfg, history)
	historyCM := make([]llm.ChatMessage, 0, len(history))
	for _, h := range history {
		historyCM = append(historyCM, llm.ChatMessage{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	msgs := []llm.ChatMessage{{Role: "system", Content: sysPrompt}}
	for _, h := range historyCM {
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
	log.Printf("[LLM] hybrid stream request: model=%s messages=%d stream=true", m.h.Cfg.LLMModel, len(msgs))
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
	var accContent string
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

		// Accumulate the streamed content for persistence
		if chunk := clientExtractedContent(payload); chunk != "" {
			accContent += chunk
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

	persistStreamResponse(m, r, conv, accContent)
	w.Write([]byte("event: done\ndata: {" + responseData + "}\n\n"))

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
