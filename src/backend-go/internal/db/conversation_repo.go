package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DBMessage is a single chat turn stored per conversation.
type DBMessage struct {
	ID         int64            `json:"id"`
	ConID      int64            `json:"conversation_id"`
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	Key        string           `json:"key"`
	Sources    []string         `json:"sources"`
	Confluence []any            `json:"confluence_links"`
	ToolCalls  []map[string]any `json:"tool_calls"`
	ToolCallID string           `json:"tool_call_id"`
	CreatedAt  time.Time        `json:"created_at"`
}

// DBConversation is a user's conversation with its persisted messages.
type DBConversation struct {
	ID        int64       `json:"id"`
	UserID    int64       `json:"user_id"`
	Title     string      `json:"title"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Messages  []DBMessage `json:"messages"`
}

// ConvView is the public, serialisable shape of a conversation (mirrors the
// frontend Conversation type: id, title, messages).
type ConvView struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
	Messages  []DBMessage `json:"messages"`
}

// ConversationRepo is the data access object for per-user chat history.
type ConversationRepo struct {
	db *DB
}

func newConversationRepo(db *DB) *ConversationRepo {
	return &ConversationRepo{db: db}
}

// ConvIDToSeq parses an integer id from a "CONV-<n>" style id or a bare number.
func ConvIDToSeq(raw string) (int64, error) {
	raw = trimPrefix(raw)
	if raw == "" {
		return 0, errors.New("empty conversation id")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid conversation id %q", raw)
	}
	return id, nil
}

func trimPrefix(s string) string {
	const p = "CONV-"
	if len(s) > len(p) && s[:len(p)] == p {
		return s[len(p):]
	}
	return s
}

// list returns all of a user's conversations (without messages).
func (cr *ConversationRepo) list(userID int64) ([]DBConversation, error) {
	rows, err := cr.db.db.QueryContext(context.Background(), `
		SELECT id, user_id, title, created_at, updated_at
		FROM conversations WHERE user_id = ?
		ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DBConversation
	for rows.Next() {
		var c DBConversation
		var cAt, uAt string
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &cAt, &uAt); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, cAt); err == nil {
			c.CreatedAt = t
		}
		if t, err := time.Parse(time.RFC3339, uAt); err == nil {
			c.UpdatedAt = t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// loadMessages populates c.Messages from the messages table.
func (cr *ConversationRepo) loadMessages(c *DBConversation) error {
	rows, err := cr.db.db.QueryContext(context.Background(), `
		SELECT id, conversation_id, role, content, key, sources, confluence, tool_calls, tool_call_id, created_at
		FROM messages WHERE conversation_id = ? ORDER BY rowid`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return err
		}
		c.Messages = append(c.Messages, m)
	}
	return rows.Err()
}

// ListConversations returns the user's conversations with their messages.
func (cr *ConversationRepo) ListConversations(userID int64) ([]ConvView, error) {
	convs, err := cr.list(userID)
	if err != nil {
		return nil, err
	}
	views := make([]ConvView, 0, len(convs))
	for i := range convs {
		if err := cr.loadMessages(&convs[i]); err != nil {
			return nil, err
		}
		views = append(views, toConvView(&convs[i]))
	}
	return views, nil
}

// GetConversation returns a single conversation (with messages) for a user.
func (cr *ConversationRepo) GetConversation(userID int64, convID string) (ConvView, error) {
	id, err := ConvIDToSeq(convID)
	if err != nil {
		return ConvView{}, err
	}
	var c DBConversation
	var cAt, uAt string
	row := cr.db.db.QueryRowContext(context.Background(), `
		SELECT id, user_id, title, created_at, updated_at
		FROM conversations WHERE id = ? AND user_id = ?`, id, userID)
	if err := row.Scan(&c.ID, &c.UserID, &c.Title, &cAt, &uAt); err != nil {
		if err == sql.ErrNoRows {
			return ConvView{}, ErrNotFound
		}
		return ConvView{}, err
	}
	if t, err := time.Parse(time.RFC3339, cAt); err == nil {
		c.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, uAt); err == nil {
		c.UpdatedAt = t
	}
	if err := cr.loadMessages(&c); err != nil {
		return ConvView{}, err
	}
	return toConvView(&c), nil
}

// CreateConversation creates a new conversation for a user.
func (cr *ConversationRepo) CreateConversation(userID int64, title string) (ConvView, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if title == "" {
		title = "Untitled"
	}
	res, err := cr.db.db.ExecContext(context.Background(),
		"INSERT INTO conversations (user_id, title, created_at, updated_at) VALUES (?, ?, ?, ?)",
		userID, title, now, now)
	if err != nil {
		return ConvView{}, err
	}
	id, _ := res.LastInsertId()
	var c DBConversation
	c.ID = id
	c.UserID = userID
	c.Title = title
	c.CreatedAt, c.UpdatedAt = time.Now(), time.Now()
	return toConvView(&c), nil
}

// DeleteMany removes multiple conversations owned by userID in a single
// transaction. The provided ids may be "CONV-<n>" style ids or bare numbers;
// non-integer and unknown ids are ignored rather than treated as errors.
// It returns the number of conversations actually deleted.
func (cr *ConversationRepo) DeleteMany(userID int64, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var deleted int64
	tx, err := cr.db.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, raw := range ids {
		id, err := ConvIDToSeq(raw)
		if err != nil {
			continue // skip malformed ids
		}
		// Verify ownership before deleting so a caller can never touch another
		// user's conversation.
		var owner int64
		row := tx.QueryRowContext(context.Background(),
			"SELECT user_id FROM conversations WHERE id = ?", id)
		if err := row.Scan(&owner); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return 0, err
		}
		if owner != userID {
			continue
		}
		if _, err := tx.ExecContext(context.Background(),
			"DELETE FROM messages WHERE conversation_id = ?", id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(context.Background(),
			"DELETE FROM conversations WHERE id = ? AND user_id = ?", id, userID); err != nil {
			return 0, err
		}
		deleted++
	}
	return deleted, tx.Commit()
}
// DeleteConversation removes a conversation (owned by userID).
func (cr *ConversationRepo) DeleteConversation(userID int64, convID string) error {
	id, err := ConvIDToSeq(convID)
	if err != nil {
		return err
	}
	tx, err := cr.db.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(context.Background(), "DELETE FROM messages WHERE conversation_id = ?", id); err != nil {
		return err
	}
	res, err := tx.ExecContext(context.Background(), "DELETE FROM conversations WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// ClearAllConversations removes every conversation for a user.
func (cr *ConversationRepo) ClearAllConversations(userID int64) error {
	_, err := cr.db.db.ExecContext(context.Background(), "DELETE FROM messages WHERE conversation_id IN (SELECT id FROM conversations WHERE user_id = ?)", userID)
	if err != nil {
		return err
	}
	_, err = cr.db.db.ExecContext(context.Background(), "DELETE FROM conversations WHERE user_id = ?", userID)
	if err != nil {
		return err
	}
	return nil
}

// AppendMessage inserts a message into a conversation owned by the user.
func (cr *ConversationRepo) AppendMessage(userID int64, convID, role, content, key string, sources []string, confluenceLinks []any, toolCalls []map[string]any, toolCallID string) error {
	id, err := ConvIDToSeq(convID)
	if err != nil {
		return err
	}

	// Verify ownership.
	var owner int64
	row := cr.db.db.QueryRowContext(context.Background(), "SELECT user_id FROM conversations WHERE id = ?", id)
	if err := row.Scan(&owner); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	if owner != userID {
		return ErrNotFound
	}

	srcs := marshalJSON(sources)
	convfluence := marshalJSON(confluenceLinks)
	toolJSON := marshalJSON(toolCalls)

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = cr.db.db.ExecContext(context.Background(),
		"INSERT INTO messages (conversation_id, role, content, key, sources, confluence, tool_calls, tool_call_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, role, content, key, srcs, convfluence, toolJSON, toolCallID, now)
	if err != nil {
		return err
	}
	// Seed a readable conversation title from the first user message.
	// This restores the pre-multi-user behaviour: the frontend uses the
	// stored title as the sidebar label for a fresh conversation. Only the
	// first user turn sets the title; assistant turns never rename it.
	_, _ = cr.db.db.ExecContext(context.Background(),
		"UPDATE conversations SET title = ? WHERE id = ? AND title = 'Untitled'",
		titleFromUserMessage(content), id)
	_, _ = cr.db.db.ExecContext(context.Background(), "UPDATE conversations SET updated_at = ? WHERE id = ?", now, id)
	return nil
}

// --- helpers ---

// titleFromUserMessage derives a short, readable conversation title from the
// content of the first user message in a conversation. It mirrors the pre-
// multi-user in-memory backend: words are collapsed (whitespace squeezed), an
// empty result falls back to "Untitled", and text longer than 60 chars is
// truncated with a trailing ellipsis.
func titleFromUserMessage(content string) string {
	preview := strings.Join(strings.Fields(content), " ")
	if preview == "" {
		return "Untitled"
	}
	if len(preview) > 60 {
		preview = preview[:60] + "…"
	}
	return preview
}
func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalSource decodes a JSON-encoded []string stored in the database,
// defaulting to an empty slice rather than nil.
func unmarshalSource(s string, dst *[]string) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		*dst = []string{}
		return
	}
	if err := json.Unmarshal([]byte(s), dst); err != nil || dst == nil {
		*dst = []string{}
	}
}
func scanMessage(s scanner) (DBMessage, error) {
	var (
		m           DBMessage
		conID       int64
		sources     string
		convfluence []byte
		toolCalls   string
		toolCallID  string
		createdAt   string
	)
	if err := s.Scan(&m.ID, &conID, &m.Role, &m.Content, &m.Key, &sources, &convfluence, &toolCalls, &toolCallID, &createdAt); err != nil {
		return m, err
	}
	if m.ID == 0 {
		return m, errors.New("message scan returned zero id")
	}
	unmarshalSource(sources, &m.Sources)
	if len(m.Confluence) == 0 && len(convfluence) > 0 {
		_ = json.Unmarshal(convfluence, &m.Confluence)
	}
	// tool_calls defaults to "[]" in the schema; unmarshal only if non-empty.
	if toolStr := strings.TrimSpace(toolCalls); toolStr != "" && toolStr != "null" && toolStr != "[]" {
		if err := json.Unmarshal([]byte(toolStr), &m.ToolCalls); err != nil {
			m.ToolCalls = nil
		}
	}
	m.ConID = conID
	return m, nil
}

func toConvView(c *DBConversation) ConvView {
	v := ConvView{
		ID:        fmt.Sprintf("%d", c.ID),
		Title:     c.Title,
		CreatedAt: "",
		UpdatedAt: "",
	}
	if !c.CreatedAt.IsZero() {
		v.CreatedAt = c.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !c.UpdatedAt.IsZero() {
		v.UpdatedAt = c.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if c.Messages == nil {
		v.Messages = []DBMessage{}
	} else {
		v.Messages = c.Messages
	}
	return v
}
