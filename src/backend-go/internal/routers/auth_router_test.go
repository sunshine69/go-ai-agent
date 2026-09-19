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

// TestAuthRouterEndToEnd wires a real *db.DB into routers.Handlers (verifying
// item 3 of the handoff — main.go DB wiring — at the router layer) and drives
// the registered auth endpoints with an httptest.Server (verifying item 4 —
// the new routes are registered and functional). This does NOT rely on the
// process listening on a TCP socket, avoiding any network/server-start flakiness.
func TestAuthRouterEndToEnd(t *testing.T) {
	d, err := db.Open(":memory:", "sqlite3")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	if _, err := d.SeedAdmin("admin", "admin@example.com", "1qa2ws"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	handlers := Handlers{
		DB:  d,
		Cfg: config.Load(""),
	}
	mux := handlers.ServeMux()

	// Sanity check: the routes must actually be registered (item 4).
	requireRegistered := func(t *testing.T, path, method string) {
		t.Helper()
		// Send an OPTIONS preflight so we exercise the mux even for methods the
		// handler might not explicitly allow; a 404 (not 405) confirms routing.
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, path, nil)
		mux.ServeHTTP(rr, req)
		if rr.Code == http.StatusNotFound {
			t.Errorf("route %s %s is not registered (404)", method, path)
		}
	}
	for _, r := range []struct{ path, method string }{
		{"/api/auth/logout", http.MethodPost},
		{"/api/auth/users", http.MethodGet},
		{"/api/auth/users", http.MethodPost},
		{"/api/auth/users/", http.MethodDelete},
		{"/api/auth/me/profile", http.MethodGet},
		{"/api/auth/me/profile", http.MethodPatch},
		{"/api/auth/me/profile/password", http.MethodPost},
	} {
		requireRegistered(t, r.path, r.method)
	}

	// Self-registration is disabled: the register endpoint must return 403.
	regBody := `{"login_name":"alice","email":"a@example.com","password":"password123"}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("register: expected 403 (disabled), got %d (%s)", rr.Code, rr.Body.String())
	}

	// Log in as admin to obtain a valid token.
	loginBody := `{"username":"admin","password":"1qa2ws"}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
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

	// Use the admin-only endpoint to create the non-admin user "alice".
	createBody := `{"login_name":"alice","email":"a@example.com","password":"password123","is_admin":false}`
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/users", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("create user: status=%d body=%s", rr.Code, rr.Body.String())
	}

	// --- GET /api/auth/me with the token (admin) ---
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("me: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "admin") {
		t.Fatalf("me body missing admin username: %s", rr.Body.String())
	}

	// --- GET /api/auth/users with the admin token (admin CAN access it) ---
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/auth/users", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("users: expected 200 (admin), got %d (%s)", rr.Code, rr.Body.String())
		t.Fatalf("users as non-admin: expected 403, got %d", rr.Code)
	}

	// --- GET /api/auth/me/profile with token ---
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me/profile", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("me/profile: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestProtectedRoutesRequireToken guards the regression where the "brain" and
// read endpoints (/api/messages*, /api/chat/stream, /api/domains) accepted an
// anonymous caller: resolveConversation used currentUserIDOr(r, 0), letting a
// no-token request silently resolve user id 0 against the datastore. After the
// requireAuth wiring at the mux layer, every one of those routes must reject an
// anonymous request with 401.
func TestProtectedRoutesRequireToken(t *testing.T) {
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

	// anonymous requests to the protected routes must be rejected (401), not
	// silently allowed through as user 0 (a 404 would mean the route is
	// unregistered, which is a different problem we don't want here).
	protected := []string{
		"/api/domains",
		"/api/messages",
		"/api/messages/stream",
		"/api/chat/stream",
		"/api/conversations",
	}
	for _, p := range protected {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: expected 401 for anonymous request, got %d (%s)", p, rr.Code, rr.Body.String())
		}
	}
}
