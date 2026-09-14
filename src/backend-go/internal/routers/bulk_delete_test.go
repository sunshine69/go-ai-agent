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

// TestBulkDeleteConversationsRouteRegistered verifies the SPA multi-select
// delete flow: POST /api/conversations/bulk-delete with {"ids":[...]}.
//
// The endpoint is owner-scoped, returns the count of deleted conversations,
// silently ignores ids that are not owned by the caller, and leaves the
// remaining conversations intact.
func TestBulkDeleteConversationsRouteRegistered(t *testing.T) {
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

	// seed three conversations for uid 1 (id 1 = the admin user)
	for _, title := range []string{"conv-a", "conv-b", "conv-c"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/conversations",
			strings.NewReader(`{"title":"`+title+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("create %s: status=%d body=%s", title, rr.Code, rr.Body.String())
		}
	}

	// THE bulk-delete request: remove two of the three by id.
	reqIDs := `{"ids":["1","2"]}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/conversations/bulk-delete",
		strings.NewReader(reqIDs))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bulk-delete: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var bodyResp struct {
		Message string `json:"message"`
		Deleted int64  `json:"deleted"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &bodyResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if bodyResp.Deleted != 2 {
		t.Fatalf("expected deleted=2, got %d (body=%s)", bodyResp.Deleted, rr.Body.String())
	}

	// verify: only "conv-c" (id 3) remains
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	var remaining []db.ConvView
	if err := json.Unmarshal(rr.Body.Bytes(), &remaining); err != nil {
		t.Fatalf("decode remaining: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining conversation, got %d (%s)", len(remaining), rr.Body.String())
	}
	if remaining[0].ID != "3" {
		t.Fatalf("expected remaining id 3, got %s", remaining[0].ID)
	}
}

// TestBulkDeleteIgnoresForeignIds verifies that an id not owned by the caller
// (malformed or belonging to another user) is silently ignored, never
// deleted, and not an error.
func TestBulkDeleteIgnoresForeignIds(t *testing.T) {
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

	// login for uid 1
	loginBody := `{"username":"admin","password":"1qa2ws"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rr, req)
	var loginResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &loginResp); err != nil || loginResp.AccessToken == "" {
		t.Fatalf("login did not return a valid access token: %v", err)
	}

	// uid 1 creates one conversation (id 1)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/conversations",
		strings.NewReader(`{"title":"mine"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create: status=%d", rr.Code)
	}

	// bulk-delete with a non-integer id and an out-of-range id; nothing is
	// deleted and the call still succeeds.
	reqIDs := `{"ids":["not-a-number","999999"]}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/conversations/bulk-delete",
		strings.NewReader(reqIDs))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("bulk-delete foreign: status=%d body=%s", rr.Code, rr.Body.String())
	}
	var bodyResp struct {
		Deleted int64 `json:"deleted"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &bodyResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bodyResp.Deleted != 0 {
		t.Fatalf("expected deleted=0 for foreign/malformed ids, got %d", bodyResp.Deleted)
	}

	// sanity: the caller's conversation is still present.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	var remaining []db.ConvView
	if err := json.Unmarshal(rr.Body.Bytes(), &remaining); err != nil {
		t.Fatalf("decode remaining: %v", err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected caller's conversation still present, got %d", len(remaining))
	}
}

// TestBulkDeleteRequiresAuth verifies the endpoint is guarded by auth.
func TestBulkDeleteRequiresAuth(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/bulk-delete",
		strings.NewReader(`{"ids":["1"]}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d body=%s", rr.Code, rr.Body.String())
	}
}
