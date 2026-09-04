package routers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stevek/go-ai-agent/backend-go/internal/config"
	"github.com/stevek/go-ai-agent/backend-go/internal/db"
)

// TestConversationClickBlankOnEndToEnd reproduces the exact client flow
// described in the bug report:
//
//	1. login -> obtain a token for user 1
//	2. create a conversation (server assigns numeric id)
//	3. POST /api/messages to persist a user+assistant turn in that conversation
//	4. GET /api/conversations/{id} (what the SPA's handleConversationSelect
//	   calls on click)
//
// The bug: clicking a conversation in the sidebar yields a blank chat because
// the GET-by-id response has no messages (or no rows at all).
func TestConversationClickBlankOnEndToEnd(t *testing.T) {
	d, err := db.Open(":memory:", "sqlite3")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	if _, err := d.SeedAdmin("admin", "admin@example.com", "1qa2ws"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	handlers := Handlers{DB: d, Cfg: config.Load("")}
	mux := handlers.ServeMux()

	// 1. login
	loginBody := `{"username":"admin","password":"1qa2ws"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var loginResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &loginResp); err != nil || loginResp.AccessToken == "" {
		t.Fatalf("login did not return a valid access token: %v", err)
	}
	t.Logf("login ok, uid=1 token=%s", loginResp.AccessToken[:16]+"...")

	// 2. create a conversation
	convReqBody := `{"title":"test conv"}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(convReqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create conversation: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var newConv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &newConv); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if newConv.ID == "" {
		t.Fatalf("new conversation has empty id")
	}
	t.Logf("created conversation id=%q", newConv.ID)

	// 3. POST /api/messages to persist a turn (stub LLM returns canned answer)
	// NOTE: this requires a working LLM. We'll instead directly call the repo
	// to append a message so we isolate the GET-by-id logic.
	if err := d.Conversations.AppendMessage(1, newConv.ID, "user", "What is the return policy?", "__current_user__:What is the return policy?", []string{}, nil); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if err := d.Conversations.AppendMessage(1, newConv.ID, "assistant", "You can return items within 30 days.", "", nil, nil); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	t.Log("persisted user+assistant messages")

	// 4. GET /api/conversations/{id} — this is what the SPA calls on click
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/conversations/"+newConv.ID, nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET by id: status=%d body=%s", rr.Code, rr.Body.String())
	}
	t.Logf("GET by id body: %s", rr.Body.String())
	var conv map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &conv); err != nil {
		t.Fatalf("decode conversation by id: %v", err)
	}
	msgs, _ := conv["messages"].([]any)
	t.Logf("GET by id returned %d messages", len(msgs))
	if len(msgs) == 0 {
		t.Fatalf("BUG REPRODUCED: GET by id returned no messages")
	}
}

// TestConversationCrossUserIsolation verifies that user 2 cannot read user 1's
// conversation (which would return blank when the SPA clicks it).
func TestConversationCrossUserIsolation(t *testing.T) {
	d, err := db.Open(":memory:", "sqlite3")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	if _, err := d.SeedAdmin("admin", "admin@example.com", "1qa2ws"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	handlers := Handlers{DB: d, Cfg: config.Load("")}
	mux := handlers.ServeMux()

	// login as admin -> uid 1
	loginBody := `{"username":"admin","password":"1qa2ws"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rr, req)
	var loginResp struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(rr.Body.Bytes(), &loginResp)
	t.Logf("uid1 token=%s", loginResp.AccessToken[:16]+"...")

	// create a conversation for user 1
	convReqBody := `{"title":"my conv"}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/conversations", strings.NewReader(convReqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	var newConv struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rr.Body.Bytes(), &newConv)
	t.Logf("uid1 created conversation id=%q", newConv.ID)

	// Now login as admin again, but pretend to be a *different* user by
	// manually issuing a token for uid 2.
	altToken, err := db.IssueToken(2, db.TokenExpiry)
	if err != nil {
		t.Fatalf("issue alt token: %v", err)
	}

	// GET the conversation with uid 2's token -> must be 404 (not found)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/conversations/"+newConv.ID, nil)
	req.Header.Set("Authorization", "Bearer "+altToken)
	mux.ServeHTTP(rr, req)
	t.Logf("uid2 GET uid1 conv: status=%d body=%s", rr.Code, rr.Body.String())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("uid2 should get 404 on uid1's conversation, got %d", rr.Code)
	}
}
