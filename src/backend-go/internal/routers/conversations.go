package routers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// conversation mirrors the Python conversation dict shape.
type conversation struct {
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

var (
	convMu  sync.Mutex
	convSeq int
	convs   = map[string]*conversation{}
)

// loadConversations restores persisted conversations from the store file (if
// CONVERSATIONS_STORE_PATH points at an existing one), mirroring the Python
// backend's single load at import time.
func loadConversations() {
	path := os.Getenv("CONVERSATIONS_STORE_PATH")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var loaded map[string]*conversation
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}
	for _, c := range loaded {
		if c == nil {
			continue
		}
		convs[c.ID] = c
		var n int
		if _, err := fmt.Sscanf(c.ID, "CONV-%04d", &n); err == nil && n > convSeq {
			convSeq = n
		}
	}
}

func newConversationID() string {
	convSeq++
	return fmt.Sprintf("CONV-%04d", convSeq)
}

func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func persistConversations() {
	path := os.Getenv("CONVERSATIONS_STORE_PATH")
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(convs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

type conversationsHandler struct{}

func newConversationsHandler() *conversationsHandler {
	return &conversationsHandler{}
}

// convView returns the public view of a conversation used by list/create/get.
func convView(c *conversation) conversation {
	if c.Messages == nil {
		return conversation{
			ID:        c.ID,
			Title:     c.Title,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
			Messages:  []message{},
		}
	}
	// Strip the internal key field from messages in the view.
	msgs := make([]message, 0, len(c.Messages))
	for _, m := range c.Messages {
		mm := m
		mm.Key = ""
		msgs = append(msgs, mm)
	}
	return conversation{
		ID:        c.ID,
		Title:     c.Title,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
		Messages:  msgs,
	}
}

// handleListAndCreate serves both GET (list) and POST (create).
func (h *conversationsHandler) handleListAndCreate(w http.ResponseWriter, r *http.Request) {
	convMu.Lock()
	defer convMu.Unlock()

	switch r.Method {
	case http.MethodGet:
		items := make([]conversation, 0, len(convs))
		for _, c := range convs {
			items = append(items, convView(c))
		}
		sortByUpdated(items)
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var body struct {
			Title string `json:"title"`
		}
		_ = decodeBody(r, &body)
		title := strings.TrimSpace(body.Title)
		if title == "" {
			title = "New Conversation"
		}
		now := nowISO()
		c := &conversation{
			ID:        newConversationID(),
			Title:     title,
			CreatedAt: now,
			UpdatedAt: now,
			Messages:  []message{},
		}
		convs[c.ID] = c
		persistConversations()
		writeJSON(w, http.StatusOK, convView(c))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleByID handles GET and DELETE for a specific conversation.
func (h *conversationsHandler) handleByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/conversations/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing conversation id")
		return
	}
	convMu.Lock()
	defer convMu.Unlock()

	switch r.Method {
	case http.MethodGet:
		c, ok := convs[id]
		if !ok {
			writeError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		writeJSON(w, http.StatusOK, convView(c))
	case http.MethodDelete:
		if _, ok := convs[id]; !ok {
			writeError(w, http.StatusNotFound, "Conversation not found")
			return
		}
		delete(convs, id)
		persistConversations()
		writeJSON(w, http.StatusOK, map[string]string{"message": "Conversation deleted"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleClearAll serves DELETE for /api/conversations with no id.
func (h *conversationsHandler) handleClearAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	convMu.Lock()
	defer convMu.Unlock()
	count := len(convs)
	convs = map[string]*conversation{}
	convSeq = 0
	persistConversations()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Conversations cleared",
		"deleted": count,
	})
}

func sortByUpdated(items []conversation) {
	// Simple insertion sort preserving relative order for equal timestamps.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j-1].UpdatedAt < items[j].UpdatedAt; j-- {
			items[j-1], items[j] = items[j], items[j-1]
		}
	}
}

// createNewConversation creates and persists a brand-new conversation, mirroring
// the Python create_conversation() helper.
func createNewConversation() *conversation {
	convMu.Lock()
	defer convMu.Unlock()

	cid := newConversationID()
	now := nowISO()
	c := &conversation{
		ID:        cid,
		Title:     "New Conversation",
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []message{},
	}
	convs[cid] = c
	persistConversations()
	return c
}

// getConversation returns the conversation by id, or nil if unknown — mirroring
// the Python conversation() helper which returns None for unknown ids.
func getConversation(id string) *conversation {
	convMu.Lock()
	defer convMu.Unlock()
	return convs[id]
}

// appendMessage persists a single turn (user/assistant) to a conversation,
// tagging the user turn with a sentinel key so the backend can exclude it from
// the next turn's history replay (mirrors the Python backend).
func appendMessage(id, role, content, key string, sources []string, confluenceLinks []confluenceLink) {
	convMu.Lock()
	defer convMu.Unlock()

	c := convs[id]
	if c == nil {
		return
	}
	c.Messages = append(c.Messages, message{
		Role:            role,
		Content:         content,
		Key:             key,
		Sources:         sources,
		ConfluenceLinks: confluenceLinks,
	})
	// Mirror the Python backend: seed a readable title from the first user
	// message instead of leaving the generic "New Conversation" label.
	if role == "user" && c.Title == "New Conversation" {
		preview := strings.Join(strings.Fields(content), " ")
		if len(preview) > 60 {
			preview = preview[:60] + "…"
		} else if preview == "" {
			preview = "Untitled"
		}
		c.Title = preview
	}
	c.UpdatedAt = nowISO()
	persistConversations()
}
