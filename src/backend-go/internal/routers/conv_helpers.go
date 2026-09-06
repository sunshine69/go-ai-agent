// Package routers — conv_helpers.go: DB-backed conversation resolution and
// message persistence used by messages.go and messages_stream.go.
//
// The multi-user rewrite replaced the old global in-memory conversation map
// with a per-user datastore (db.ConversationRepo). These helpers bridge that
// gap so the message handlers can resolve a conversation by id (ownership
// scoped to the authenticated caller) or create a new one, and persist turns.
package routers

import (
	"net/http"

	"github.com/stevek/go-ai-agent/backend-go/internal/db"
)

// derefStr safely dereferences a *string, returning "" when nil.
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// resolveConversation resolves a conversation for the current user.
//
//   - When cid != "" and the conversation is owned by the caller, it returns
//     that conversation.
//   - Otherwise a new conversation is created for the caller and returned.
//
// It returns the full ConvView (with messages) and false when no datastore is
// attached to the handlers (i.e. the DB-backed endpoints are disabled).
func resolveConversation(h Handlers, r *http.Request, cid string) db.ConvView {
	dbStore := h.DB
	if dbStore == nil || dbStore.Conversations == nil {
		return db.ConvView{}
	}
	uid := currentUserIDOr(r, 0)
	if cid != "" {
		if existing, err := dbStore.Conversations.GetConversation(uid, cid); err == nil {
			return existing
		}
	}
	conv, _ := dbStore.Conversations.CreateConversation(uid, "")
	return conv
}

// convExists reports whether the resolved conversation id is a real value.
func convExists(conv db.ConvView) bool {
	return conv.ID != ""
}

// persistMessage stores a single turn (user or assistant) in the resolved
// conversation, scoped to the caller. It is best-effort: persistence failures
// are swallowed so the LLM answer is still delivered.
//
// For an assistant turn, toolCalls records the OpenAI-style tool_calls the model
// emitted that turn (the SPA replays them when the user reopens the
// conversation). toolCallID links a subsequent "tool" role turn to its
// originating "assistant" tool_calls entry so tool-use turns are stored in
// chronological order in the returned history.
func persistMessage(h Handlers, r *http.Request, conv db.ConvView, role, content, key string, sources []string, toolCalls []map[string]any, toolCallID string) {
	dbStore := h.DB
	if dbStore == nil || dbStore.Conversations == nil || conv.ID == "" {
		return
	}
	uid := currentUserIDOr(r, 0)
	convfluence := make([]any, 0, len(conv.Messages))
	for _, m := range conv.Messages {
		convfluence = append(convfluence, m.Confluence)
	}
	_ = dbStore.Conversations.AppendMessage(uid, conv.ID, role, content, key, sources, convfluence, toolCalls, toolCallID)
}
