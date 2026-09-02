// Package routers — conversations.go: DB-backed, per-user conversation history.
//
// GET /api/conversations           (authed)  -> user's conversation list
// POST /api/conversations          (authed)  -> {id,title,created_at,updated_at}
// GET /api/conversations/{id}      (authed)  -> full conversation (only its own)
// DELETE /api/conversations/{id}   (authed)  -> ok (only its own)
// DELETE /api/conversations        (authed)  -> clear user's own conversations
//
// Every conversation is owned by a single user id; other users cannot read,
// modify, or delete it. The conversation id returned to the client is the raw
// integer primary key so the frontend can pass it straight back.
package routers

import (
	"net/http"
	"time"
)

type conversationsHandler struct {
	db *db.DB
}

func newConversationsHandler(db *db.DB) *conversationsHandler {
	return &conversationsHandler{db: db}
}

// convPublicView is the serialisable view of a conversation returned to the
// frontend (id, title, timestamps, messages).
type convPublicView struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role            string           `json:"role"`
	Content         string           `json:"content"`
	Key             string           `json:"key,omitempty"`
	Sources         []string         `json:"sources,omitempty"`
	ConfluenceLinks []confluenceLink `json:"confluence_links,omitempty"`
}

// handleListAndCreate serves GET (list) and POST (create) for the caller.
func (h *conversationsHandler) handleListAndCreate(w http.ResponseWriter, r *http.Request) {
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	switch r.Method {
	case http.MethodGet:
		views, err := h.db.Conversations.ListConversations(uid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list conversations")
			return
		}
		if views == nil {
			views = []convPublicView{}
		}
		// Emit a list of lightweight summaries (no full messages) so the
		// sidebar stays cheap; the full list is fetched via the GET by id.
		writeJSON(w, http.StatusOK, views)
	case http.MethodPost:
		var body struct {
			Title string `json:"title"`
		}
		_ = decodeBody(r, &body)
		view, err := h.db.Conversations.CreateConversation(uid, body.Title)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create conversation")
			return
		}
		writeJSON(w, http.StatusOK, h.toPublicView(view))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleByID handles GET and DELETE for a specific conversation. Ownership is
// enforced: a user can only touch their own conversation.
func (h *conversationsHandler) handleByID(w http.ResponseWriter, r *http.Request) {
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := trimPrefix(r.URL.Path, "/api/conversations/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing conversation id")
		return
	}

	switch r.Method {
	case http.MethodGet:
		view, err := h.db.Conversations.GetConversation(uid, id)
		if err != nil {
			writeError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		writeJSON(w, http.StatusOK, h.toPublicView(view))
	case http.MethodDelete:
		if err := h.db.Conversations.DeleteConversation(uid, id); err != nil {
			writeError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "Conversation deleted"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleClearAll serves DELETE for /api/conversations with no id (clears the
// caller's own conversations).
func (h *conversationsHandler) handleClearAll(w http.ResponseWriter, r *http.Request) {
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := h.db.Conversations.ClearAllConversations(uid); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear conversations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Conversations cleared",
	})
}

func (h *conversationsHandler) toPublicView(v db.ConvView) convPublicView {
	pv := convPublicView{
		ID:        v.ID,
		Title:     v.Title,
		CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt,
		Messages:  []message{},
	}
	if v.CreatedAt == "" {
		pv.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if v.UpdatedAt == "" {
		pv.UpdatedAt = pv.CreatedAt
	}
	for _, m := range v.Messages {
		pv.Messages = append(pv.Messages, message{
			Role:            m.Role,
			Content:         m.Content,
			Key:             m.Key,
			Sources:         m.Sources,
			ConfluenceLinks: m.Confluence,
		})
	}
	return pv
}

// trimPrefix returns s with prefix removed from the front if present.
func trimPrefix(s, prefix string) string {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
