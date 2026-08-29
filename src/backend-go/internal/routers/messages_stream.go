package routers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	ctxpkg "github.com/stevek/go-ai-agent/backend-go/internal/context"
	"github.com/stevek/go-ai-agent/backend-go/internal/llm"
)

// messageStreamHandler exposes POST /api/messages/stream, an SSE (server-sent
// events) endpoint that mirrors handle() but streams the LLM answer to the
// client token-by-token instead of waiting for the full completion.
//
// SSE shape (RFC 3870, one event per line):
//
//	context:{json}      -> {conversation_id, sources, confluence_links, system_prompt}
//	token:<text>        -> incremental assistant text chunk
//	finished:{"conversation_id","answer","sources","confluence_links"}
//	error:{"message":"..."}
//
// This lets the frontend (Wails React, Streamlit) render the answer as it
// arrives. The client only needs to send the same fields as POST /api/messages.
type messageStreamHandler struct {
	h Handlers
}

func newMessagesStreamHandler(h Handlers) *messageStreamHandler {
	return &messageStreamHandler{h: h}
}

// streamContext is the JSON payload sent as the first SSE "context" event.
type streamContext struct {
	ConID        string           `json:"conversation_id"`
	Sources      []string         `json:"sources"`
	Confluence   []confluenceLink `json:"confluence_links"`
	SystemPrompt string           `json:"system_prompt"`
}

// streamFinal mirrors messageResponse: the completed answer plus its metadata,
// sent once streaming is done.
type streamFinal struct {
	ConID      string           `json:"conversation_id"`
	Answer     string           `json:"answer"`
	Sources    []string         `json:"sources"`
	Confluence []confluenceLink `json:"confluence_links"`
}

func (m *messageStreamHandler) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeSSE(w, "error", map[string]string{"message": "method not allowed"})
		return
	}

	var req messageRequest
	if err := decodeBody(r, &req); err != nil {
		writeSSE(w, "error", map[string]string{"message": "invalid request body"})
		return
	}

	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeSSE(w, "error", map[string]string{"message": "message is required"})
		return
	}

	// Resolve the conversation (mirrors handle()).
	var convID string
	if cid := req.ConID; cid != "" {
		if existing := getConversation(cid); existing != nil {
			convID = existing.ID
		}
	}
	if convID == "" {
		convID = createNewConversation().ID
	}

	// Build prior turns for the LLM (mirror handle()).
	userKey := "__current_user__:" + msg
	history := []message{}
	conv := getConversation(convID)
	for _, turn := range conv.Messages {
		if turn.Role == "user" && turn.Key == userKey {
			continue
		}
		history = append(history, turn)
	}

	// --- Gather context from MCP + RAG (same as handle()) ------------------
	builder := ctxpkg.New(m.h.Cfg, m.h.Manager, m.h.Rag)
	contextText, sources, confluenceRefs := builder.BuildContext(msg, req.Domain, req.SubCategory)
	if detectNoUsefulContext(contextText, sources) {
		contextText = ""
	}

	confluenceBaseURL := strings.TrimSuffix(m.h.Cfg.ConfluenceBaseURL, "/")
	confluenceLinks := []confluenceLink{}
	if confluenceBaseURL != "" {
		for _, ref := range confluenceRefs {
			confluenceLinks = append(confluenceLinks, confluenceLink{
				Title: ref["title"],
				URL:   confluenceBaseURL + "/pages/viewpage.action?pageId=" + ref["id"],
			})
		}
	}
	if len(confluenceLinks) > 0 {
		var bld strings.Builder
		bld.WriteString("\n\n=== Available Confluence Links (cite using these exact URLs) ===\n")
		for _, l := range confluenceLinks {
			bld.WriteString("- [")
			bld.WriteString(l.Title)
			bld.WriteString("](")
			bld.WriteString(l.URL)
			bld.WriteString(")\n")
		}
		contextText += bld.String()
	}

	sysPrompt := systemPrompt()

	// --- Handshake: send the context block first so the client renders
	// sources/prompts while the LLM is still computing. ---------------------
	writeSSE(w, "context", streamContext{
		ConID:        convID,
		Sources:      sources,
		Confluence:   confluenceLinks,
		SystemPrompt: sysPrompt,
	})

	// --- Convert history for the LLM client --------------------------------
	chatHistory := make([]llm.ChatMessage, 0, len(history))
	for _, t := range history {
		chatHistory = append(chatHistory, llm.ChatMessage{Role: t.Role, Content: t.Content})
	}

	// --- Stream the LLM answer --------------------------------------------
	var fullAnswer strings.Builder
	sink := func(tok string) {
		fullAnswer.WriteString(tok)
		writeSSE(w, "token", map[string]string{"text": tok})
	}

	// writeDone persists the turns and sends the "finished" event exactly once,
	// after streaming completes or errors out.
	writeDone := func() {
		answer := fullAnswer.String()
		if strings.TrimSpace(answer) != "" {
			appendMessage(convID, "assistant", answer, "")
			appendMessage(convID, "user", msg, userKey)
		}
		final := streamFinal{
			ConID:      convID,
			Answer:     answer,
			Sources:    sources,
			Confluence: confluenceLinks,
		}
		if len(final.Sources) == 0 {
			final.Sources = []string{"Direct Answer"}
		}
		writeSSE(w, "finished", final)
	}

	// best-effort flush of buffered data before streaming begins.
	if f, ok := w.(http.Flusher); ok && f != nil {
		f.Flush()
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	err := m.h.LLM.AnswerStream(ctx, sysPrompt, chatHistory, contextText, msg, sink)
	if err != nil {
		writeSSE(w, "error", map[string]string{
			"message": "Sorry, I encountered an error: " + err.Error(),
		})
	}
	writeDone()

	// Final flush so the last bytes reach the client before the stream closes.
	if f, ok := w.(http.Flusher); ok && f != nil {
		f.Flush()
	}
}

// writeSSE writes one named SSE event and flushes it immediately. The writer is
// expected to support http.Flusher (standard net/http ResponseWriter does).
func writeSSE(w http.ResponseWriter, event string, payload interface{}) {
	f, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx/gzip buffering

	// Event header (blank line + newline).
	if _, err := w.Write([]byte("event: " + event + "\n\n")); err != nil {
		return
	}
	// Data payload; JSON is single-line-safe for our shapes.
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return
	}
	// Separate the event from the next with a blank line.
	if _, err := w.Write([]byte("\n")); err != nil {
		return
	}
	if f != nil {
		f.Flush()
	}
}
