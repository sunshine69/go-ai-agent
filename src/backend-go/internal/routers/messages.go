package routers

import (
	"net/http"
	"strings"

	"github.com/stevek/go-ai-agent/backend-go/internal/context"
	"github.com/stevek/go-ai-agent/backend-go/internal/db"
	"github.com/stevek/go-ai-agent/backend-go/internal/llm"
)

// messageRequest mirrors the Python MessageRequest schema.
type messageRequest struct {
	Message     string        `json:"message"`
	ConID       string        `json:"conversation_id"`
	History     []historyItem `json:"history"`
	Domain      string        `json:"domain"`
	SubCategory string        `json:"sub_category"`
}

// historyItem mirrors one prior turn in the conversation history.
type historyItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// messageResponse mirrors the Python MessageResponse schema.
type messageResponse struct {
	ConID           string           `json:"conversation_id"`
	Answer          string           `json:"answer"`
	Sources         []string         `json:"sources"`
	ConfluenceLinks []confluenceLink `json:"confluence_links"`
}

// confluenceLink mirrors a single Confluence citation.
type confluenceLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type messagesHandler struct {
	h Handlers
}

func newMessagesHandler(h Handlers) *messagesHandler {
	return &messagesHandler{h: h}
}

// handle serves POST /api/messages: resolve the conversation, gather context
// via the builder (MCP + RAG), ask the LLM, persist the turn, and return the
// response. Mirrors the Python send_message router.
func (m *messagesHandler) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req messageRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	// --- Resolve the conversation (mirrors Python send_message) -------------
	conv := resolveConversation(m.h, r, req.ConID)
	// If an explicit conversation id was sent but does not exist (e.g. it
	// belongs to another user or was deleted), resolveConversation already
	// falls back to creating a fresh one.

	// --- Optional model-driven tool-use path -------------------------------
	// If enabled and the server can call tools, serve this turn through the
	// tool-use controller instead of the hybrid ContextBuilder+text-injection
	// path. When handled (or an error occurred), stop here — otherwise fall
	// through to the hybrid path below. A nil controller (MCP disabled) or a
	// failed capability probe returns handled=false so the caller keeps the
	// current behavior.
	msh := newMessagesStreamHandler(m.h)
	if handled, _ := msh.runToolUseBlocking(r, conv, msg, req.Domain, req.SubCategory); handled {
		return
	}

	// --- Build prior turns for the LLM (cross-turn context) ----------------
	// Replay every persisted turn EXCEPT the current one (sent in the context
	// block below). Matches the Python history-building logic.
	userKey := "__current_user__:" + msg
	history := []db.DBMessage{}
	for _, turn := range conv.Messages {
		if turn.Role == "user" {
			if turn.Key == userKey {
				continue
			}
		}
		history = append(history, turn)
	}

	// --- Gather context from MCP + RAG -------------------------------------
	builder := context.New(m.h.Cfg, m.h.Manager, m.h.Rag)
	contextText, sources, confluenceRefs := builder.BuildContext(msg, req.Domain, req.SubCategory)

	// Skip context when it is empty or purely errors so the LLM can answer
	// freely based on its own knowledge.
	if detectNoUsefulContext(contextText, sources) {
		contextText = ""
	}

	// --- Convert Confluence refs to clickable links ------------------------
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
	// Give the LLM the exact URLs so it can cite them inline.
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

	// --- Ask the LLM --------------------------------------------------------
	chatHistory := make([]llm.ChatMessage, 0, len(history))
	for _, t := range history {
		chatHistory = append(chatHistory, llm.ChatMessage{Role: t.Role, Content: t.Content})
	}
	answer := m.h.LLM.Answer(r.Context(), systemPrompt(), chatHistory, contextText, msg)

	// --- Persist the current turn -----------------------------------------
	// The assistant answer is persisted separately from the user turn.
	if answer != "" {
		persistMessage(m.h, r, conv, "assistant", answer, "", sources, nil, "")
	}
	persistMessage(m.h, r, conv, "user", msg, userKey, sources, nil, "")

	// --- Return the response ------------------------------------------------
	srcs := sources
	if len(srcs) == 0 {
		srcs = []string{"Direct Answer"}
	}

	writeJSON(w, http.StatusOK, messageResponse{
		ConID:           conv.ID,
		Answer:          answer,
		Sources:         srcs,
		ConfluenceLinks: confluenceLinks,
	})
}

// systemPrompt mirrors the Python build_system_prompt() with dual identity
// support: GenIQ (knowledge mode) for the organization's knowledge base and
// Friendly Agent (casual mode) for everything else.
func systemPrompt() string {
	return `You are a helpful AI assistant with TWO identities:

## Identity 1: GenIQ (knowledge mode)
You are an expert knowledge assistant for this organization.
You answer questions about the organization's procedures, forms, skills, processes, and policies.
You are thorough, accurate, and cite sources when referencing knowledge base content.
Use the provided context to give accurate responses, always citing sources.

**How to cite RAG document sources:**
When your answer comes from RAG document snippets (shown with format "=== [Document Title] (page [X]): ..."), you MUST include the document title in your answer. For example: "According to the MRI Brochure, you should..." or "The HR Employee Handbook states...". Cite the document title every time you use information from a document.

**How to cite Confluence sources:**
When your answer comes from Confluence pages (shown with format "- [Page Title](url)"), include a link to the Confluence page. For example: "As described in the Onboarding Guide [[link]]."

**When no context is provided:**
If no document context is shown above your answer, use your general knowledge but still cite what you know.

## Identity 2: Friendly Agent (casual mode)
You are a fun, casual AI assistant.
You answer general questions, tell jokes, chat, and be helpful in everyday ways.
You are witty, friendly, and approachable — like a helpful coworker who's also funny.
You can handle anything outside the knowledge base — weather, recipes, trivia, life advice — with a light, warm tone.

## How to choose which identity
- If the question is about the organization's knowledge base, forms, procedures, or related topics, you are GenIQ.
- For everything else, you are the Friendly Agent.
- Use whichever identity feels most natural — you can seamlessly switch between modes.`
}

// detectNoUsefulContext mirrors the Python backend's detect_no_useful_context:
// returns true when the gathered context is empty or purely error-based, so the
// caller can skip it and let the LLM answer freely.
func detectNoUsefulContext(contextText string, sources []string) bool {
	if contextText == "" || strings.TrimSpace(contextText) == "" {
		return true
	}
	lines := []string{}
	for _, l := range strings.Split(contextText, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return true
	}
	// RAG snippets are present when the context includes RAG headers.
	if strings.Contains(contextText, "=== RAG Document Search") {
		return false
	}
	// Document sources (PDF/MD/docx) indicate RAG results are present.
	if len(sources) > 0 {
		docExt := []string{".pdf", ".md", ".docx", ".doc"}
		for _, s := range sources {
			sl := strings.ToLower(s)
			for _, ext := range docExt {
				if strings.HasSuffix(sl, ext) {
					return false
				}
			}
		}
	}
	// If most non-empty lines are short error strings, treat as no useful context.
	errorCount := 0
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), "error") && len(l) < 100 {
			errorCount++
		}
	}
	if errorCount >= len(lines)-1 {
		return true
	}
	return false
}
