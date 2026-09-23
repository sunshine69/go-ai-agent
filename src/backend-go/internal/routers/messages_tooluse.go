package routers

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"strings"

	ctxbldg "github.com/sunshine69/go-ai-agent/backend-go/internal/context"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/llm"
)

// augmentToolUseMessage returns a user message string with the current-turn
// knowledge-base context (RAG documents + Confluence links) prepended. It
// mirrors the message the hybrid path builds (handleStreamChat in
// messages_stream.go), so the model sees the SAME retrieved context in tool
// mode as it does in hybrid mode.
//
// This is exactly what keeps RAG enabled while tool use is on: the tool-use
// controller never calls the RAG store itself, so without this helper the
// tool path would silently drop all RAG/Confluence retrieval and the model
// would answer from its own weights only. When RAG is unavailable, the query
// is empty, or the retrieved context is empty/purely error, the original query
// is returned unchanged. It also returns the retrieved sources and Confluence
// citations so the caller can surface them in the context/done SSE events,
// mirroring the hybrid path.
func augmentToolUseMessage(r *http.Request, h Handlers, query, domain, subCategory string) (string, []string, []confluenceLink) {
	// Resolve the per-user RAG store (or the shared default) so the context
	// builder uses the correct vector index for this caller — mirroring the
	// hybrid path in messages.go. This is the key fix: previously we used
	// h.Rag directly, which always pointed at the default store and silently
	// ignored any /ragdir change the user made.
	store := h.ragManager.Client(currentUserIDOr(r, 0))
	fmt.Printf("[DEBUG RAGDIR] augmentToolUseMessage: resolving store for uid=%d => store=%v\n", currentUserIDOr(r, 0), store)
	if store == nil || query == "" {
		fmt.Printf("[DEBUG RAGDIR] augmentToolUseMessage: store nil or empty query=%q, returning early\n", query)
		return query, nil, nil
	}
	builder := ctxbldg.New(h.Cfg, h.mcpClient(r), store)
	contextText, sources, confluenceRefs := builder.BuildContext(query, domain, subCategory)
	// Skip context when it is empty or purely error-based so the LLM can answer
	// freely from its own knowledge + tools.
	if detectNoUsefulContext(contextText, sources) {
		return query, nil, nil
	}
	// Append Confluence links inline, mirroring messages.go.
	confluenceBaseURL := strings.TrimSuffix(h.Cfg.ConfluenceBaseURL, "/")
	if confluenceBaseURL != "" && len(confluenceRefs) > 0 {
		var bld strings.Builder
		bld.WriteString("\n\n=== Available Confluence Links (cite using these exact URLs) ===\n")
		for _, ref := range confluenceRefs {
			bld.WriteString("- [")
			bld.WriteString(ref["title"])
			bld.WriteString("](")
			bld.WriteString(confluenceBaseURL)
			bld.WriteString("/pages/viewpage.action?pageId=")
			bld.WriteString(ref["id"])
			bld.WriteString(")\n")
		}
		contextText += bld.String()
	}
	if strings.TrimSpace(contextText) == "" {
		return query, nil, nil
	}
	// Build citations mirroring the hybrid path (messages_stream.go) so the SPA
	// can link back to the source Confluence pages.
	citations := []confluenceLink{}
	if confluenceBaseURL != "" {
		for _, ref := range confluenceRefs {
			citations = append(citations, confluenceLink{
				Title: ref["title"],
				URL:   confluenceBaseURL + "/pages/viewpage?pageId=" + ref["id"],
			})
		}
	}
	return fmt.Sprintf(
		"Here is additional context from the knowledge base:\n\n%s\n\nPlease answer this question:\n\n%s",
		contextText, query), sources, citations
}

// persistToolTurn persists a single tool-related turn emitted by the
// tool-use controller, scoped to the caller. Two kinds of turns are persisted:
//
//   - An "assistant" turn that requested tools: its ToolCalls are stored in the
//     tool_calls column (as []map[string]any) so the next turn can replay them.
//   - A "tool" turn (role "tool"): its tool_call_id links it back to the
//     originating assistant tool_calls entry, so tool exchanges round-trip.
//
// It is best-effort: a nil datastore / empty conversation id is a silent no-op.
func persistToolTurn(h Handlers, r *http.Request, conv db.ConvView, msg *llm.ChatMessage) {
	dbStore := h.DB
	if dbStore == nil || dbStore.Conversations == nil || conv.ID == "" {
		return
	}
	if msg == nil {
		return
	}
	// Assistant tool-request: persist the tool_calls for model replay.
	if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
		persistMessage(h, r, conv, "assistant", msg.Content, "", nil, llm.ToolCallMaps(msg.ToolCalls), "")
		return
	}
	// Tool result: link back to the originating assistant tool_calls via id.
	if msg.Role == "tool" && msg.ToolCallID != "" {
		persistMessage(h, r, conv, "tool", msg.Content, "", nil, nil, msg.ToolCallID)
	}
}

// streamSink returns a sink(func(string)) that writes each streamed token to
// w as an OpenAI-style "message" SSE event, matching the shape the SPA
// (useStreaming.ts) consumes from /api/chat/stream. Tokens are flushed so the
// client receives them in real time. A token-less string is skipped.
func streamSink(w http.ResponseWriter) func(string) {
	flusher, _ := w.(http.Flusher)
	return func(token string) {
		if token == "" {
			return
		}
		eventData := fmt.Sprintf("{\"content\":\"%s\"}", escapeSSE(token))
		if _, err := w.Write([]byte("event: message\ndata: " + eventData + "\n\n")); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// writeToolUseDone writes the closing "done" SSE event carrying the resolved
// conversation id plus the RAG sources and Confluence citations retrieved for
// this turn. In tool mode these come from the same ContextBuilder used by the
// hybrid path, so the SPA sees consistent source metadata regardless of which
// path served the request.
func writeToolUseDone(w http.ResponseWriter, convID string, sources []string, citations []confluenceLink) {
	responseData := fmt.Sprintf(
		"\"conversation_id\":\"%s\",\"sources\":%v,\"citations\":%v",
		convID, formatSourcesForSSE(sources), formatCitationsForSSE(citations))
	if _, err := w.Write([]byte("event: done\ndata: {" + responseData + "}\n\n")); err != nil {
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// runToolUse serves a single user turn through the model-driven tool-use
// controller instead of the hybrid ContextBuilder path. It:
//
//  1. Builds messages from the conversation's stored history (including any
//     prior tool_calls and tool_result turns) plus the raw user message.
//  2. Streams the controller's final answer to the client token-by-token.
//  3. Persists the final answer and every tool-related turn for round-trip
//     multi-turn fidelity.
//  4. Writes the closing "done" event.
//
// It returns (handled, err): handled is true when the request was fully served
// (whether the controller succeeded or produced a self-streamed error) and the
// caller should skip the hybrid path. err is non-nil only on an internal
// failure that prevented serving; the caller may fall back to the hybrid path.
func (m *messageStreamHandler) runToolUse(w http.ResponseWriter, r *http.Request, conv db.ConvView, msg, domain, subCategory string) (bool, error) {
	// Use the request's own context: the per-request HTTP client timeout bounds
	// each turn; the loop is bounded by MODEL_MAX_TOOL_CALLS. r.Context() is
	// cancelled if the client disconnects, so no extra deadline is needed.
	toolCtx := r.Context()

	// Resolve the user's per-user LLM URL override (/url endpoint). This is
	// the critical fix: runToolUse previously ignored the override and always
	// used m.h.Cfg.LLMBASEURL, causing responses to be sent to the wrong host.
	effectiveBase := m.h.applyUserLLMURL(m.h.Cfg, r)
	log.Printf("[LLMURL] runToolUse: uid=%d cfg.LLMBASEURL=%q override=%q -> effective=%q",
		currentUserIDOr(r, 0), m.h.Cfg.LLMBASEURL, resolveLLMURL(m.h.DB, currentUserIDOr(r, 0)), effectiveBase)

	// Build the controller. A nil controller means MCP is disabled, so there is
	// no point entering tool mode — fall back to hybrid.
	ctl := newToolUseController(r, m.h, effectiveBase)
	if ctl == nil {
		return false, nil
	}
	// Probe-confirm tool-call support in "auto" mode (or honor "true"). When
	// the server cannot call tools we return false so the caller keeps serving.
	if !shouldUseToolUse(toolCtx, m.h.Cfg, m.h.LLM.WithBaseURL(effectiveBase)) {
		return false, nil
	}

	// Build messages from stored history. Persisted tool_result turns (role
	// "tool") are skipped here: the controller re-emits them from its own loop
	// tracking (result.PersistMsgs), and they carry a tool_call_id that links
	// back to the assistant tool_calls they answer.
	msgs := []llm.ChatMessage{{Role: "system", Content: resolveSystemPrompt(m.h.DB, currentUserIDOr(r, 0))}}
	for _, tm := range conv.Messages {
		if tm.Role != "user" && tm.Role != "assistant" {
			continue
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:       tm.Role,
			Content:    tm.Content,
			ToolCalls:  llm.FromToolCalls(tm.ToolCalls),
			ToolCallID: tm.ToolCallID,
		})
	}
	// In tool mode we send the raw user message: the model gathers knowledge by
	// calling tools rather than via server-injected context text.
	// Augment the query with RAG + Confluence context, mirroring the hybrid
	// path, so retrieved knowledge reaches the model even in tool mode.
	query, sources, citations := augmentToolUseMessage(r, m.h, msg, domain, subCategory)
	msgs = append(msgs, llm.ChatMessage{Role: "user", Content: query})

	// Stream the final answer to the client.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := w.Write([]byte(fmt.Sprintf(
		"event: context\nid: %d\ndata: {\"conversation_id\":\"%s\",\"sources\":%v}\n\n",
		time.Now().UnixNano(), conv.ID, formatSourcesForSSE(sources)))); err != nil {
		return true, nil
	}
	flusher, _ := w.(http.Flusher)
	_ = flusher
	sink := streamSink(w)

	result, runErr := ctl.Run(toolCtx, msgs, sink)

	// Assemble the final answer, surfacing any controller error text when the
	// loop produced no answer (e.g. it hit the safety limit).
	finalAnswer := result.FinalAnswer
	if runErr != nil && finalAnswer == "" {
		finalAnswer = "Sorry, I encountered an error: " + result.Err
	}
	if finalAnswer != "" {
		persistStreamResponse(m, r, conv, finalAnswer)
	}
	for _, pm := range result.PersistMsgs {
		persistToolTurn(m.h, r, conv, pm)
	}

	// On an internal controller failure (e.g. the model choked / returned
	// invalid params), surface it to the client as an SSE "error" event BEFORE
	// the closing "done" event. Otherwise useStreaming resets streamingState.error
	// back to null on the "done" event and flips the send button back to play —
	// the exact silent-failure bug reported: no error shown, nothing in server logs.
	if runErr != nil {
		errorData := fmt.Sprintf("{\"error\":\"%s\"}", escapeSSE(finalAnswer))
		if _, werr := w.Write([]byte("event: error\ndata: " + errorData + "\n\n")); werr != nil {
			// Best-effort: still write done below so the client can reset.
		} else if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	writeToolUseDone(w, conv.ID, sources, citations)
	return true, nil
}

// runToolUseBlocking is the non-streaming counterpart of runToolUse. It is used
// by the /api/messages handler, which must return the fully-assembled answer in
// the JSON response body instead of emitting SSE events.
// It reuses runToolUse's history/RAG logic; the only difference
// is that it calls ctl.Run with a nil sink so the loop returns the answer
func (m *messageStreamHandler) runToolUseBlocking(r *http.Request, conv db.ConvView, msg, domain, subCategory string) (bool, error) {
	// Scope the loop to a generous timeout.
	toolCtx := r.Context()

	// Resolve the user's per-user LLM URL override (/url endpoint). This is
	// the critical fix: runToolUseBlocking previously ignored the override and
	// always used m.h.Cfg.LLMBASEURL, causing responses to be sent to the wrong host.
	effectiveBase := m.h.applyUserLLMURL(m.h.Cfg, r)

	// Build the controller. A nil controller means MCP is disabled, so there is
	// no point entering tool mode — fall back to hybrid.
	ctl := newToolUseController(r, m.h, effectiveBase)
	if ctl == nil {
		return false, nil
	}
	// Probe-confirm tool-call support in "auto" mode (or honor "true"). When
	// the server cannot call tools we return false so the caller keeps serving.
	if !shouldUseToolUse(toolCtx, m.h.Cfg, m.h.LLM.WithBaseURL(effectiveBase)) {
		return false, nil
	}

	// Build messages from stored history (same as runToolUse): tool_result
	// turns (role "tool") are skipped because the controller re-emits them.
	msgs := []llm.ChatMessage{{Role: "system", Content: resolveSystemPrompt(m.h.DB, currentUserIDOr(r, 0))}}
	for _, tm := range conv.Messages {
		if tm.Role != "user" && tm.Role != "assistant" {
			continue
		}
		msgs = append(msgs, llm.ChatMessage{
			Role:       tm.Role,
			Content:    tm.Content,
			ToolCalls:  llm.FromToolCalls(tm.ToolCalls),
			ToolCallID: tm.ToolCallID,
		})
	}
	// In tool mode we send the raw user message: the model gathers knowledge by
	// calling tools rather than via server-injected context text.
	// Augment the query with RAG + Confluence context, mirroring the hybrid
	// path, so retrieved knowledge reaches the model even in tool mode.
	query, _, _ := augmentToolUseMessage(r, m.h, msg, domain, subCategory)
	msgs = append(msgs, llm.ChatMessage{Role: "user", Content: query})

	// Run with a nil sink so the loop returns the assembled final answer.
	result, runErr := ctl.Run(toolCtx, msgs, nil)

	finalAnswer := result.FinalAnswer
	if runErr != nil && finalAnswer == "" {
		finalAnswer = "Sorry, I encountered an error: " + result.Err
	}
	if finalAnswer != "" {
		persistMessage(m.h, r, conv, "assistant", finalAnswer, "", nil, nil, "")
	}
	for _, pm := range result.PersistMsgs {
		persistToolTurn(m.h, r, conv, pm)
	}
	return true, nil
}
