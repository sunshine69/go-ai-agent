package routers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/config"
	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
)

// TestClearAllConversationsRouteRegistered reproduces the exact SPA "Clear All
// Conversations" flow and verifies the endpoint is now reachable (not a 405).
//
// The bug: the SPA's handleClearAllConversations calls DELETE /api/conversations
// (no id). The route table only registered:
//
//	mux.HandleFunc("/api/conversations", conversations.handleListAndCreate) // GET/POST
//	mux.HandleFunc("/api/conversations/",  conversations.handleByID)        // GET/DELETE /id
//
// so the DELETE-without-id request fell through to handleListAndCreate, whose
// switch default returns 405 Method Not Allowed. The SPA swallows that error
// (catch {}), so clicking "Clear All Conversations" did nothing.
//
// The fix registers an explicit method-specific pattern:
//
//	mux.HandleFunc("DELETE /api/conversations", conversations.handleClearAll)
func TestClearAllConversationsRouteRegistered(t *testing.T) {
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

	// login
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

	// seed two conversations for uid 1
	for _, title := range []string{"conv-a", "conv-b"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/conversations",
			strings.NewReader(`{"title":"`+title+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("create %s: status=%d", title, rr.Code)
		}
	}

	// sanity: list before clear
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var before []db.ConvView
	if err := json.Unmarshal(rr.Body.Bytes(), &before); err != nil {
		t.Fatalf("decode before: %v", err)
	}
	if len(before) < 2 {
		t.Fatalf("expected >=2 convs before clear, got %d", len(before))
	}

	// THE CLEAR ALL request (the bug): DELETE with no id
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE /api/conversations: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// verify: list is now empty
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	var after []db.ConvView
	if err := json.Unmarshal(rr.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("after clear, expected 0 convs, got %d", len(after))
	}
	t.Log("clear all worked: conversations removed and list is empty")
}

// TestClearAllRequiresAuth verifies the clear-all endpoint is guarded by auth,
// mirroring the other conversation handlers.
func TestClearAllRequiresAuth(t *testing.T) {
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

	// no token -> 401
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/conversations", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d body=%s", rr.Code, rr.Body.String())
	}
}
