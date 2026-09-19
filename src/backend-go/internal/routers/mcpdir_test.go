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

// TestMCPdirEndpointEndToEnd wires a real *db.DB into routers.Handlers, drives
// the GET/POST /api/mcpdir endpoints with an httptest.Server (verifying the new
// routes are registered and functional), and checks the round-trip of the
// per-user mcpWorkdir setting.
func TestMCPdirEndpointEndToEnd(t *testing.T) {
	d, err := db.Open(":memory:", "sqlite3")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	user, err := d.SeedAdmin("admin", "admin@example.com", "1qa2ws")
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	handlers := Handlers{DB: d, Cfg: config.Load("")}
	mux := handlers.ServeMux()

	// Sanity check: the routes must actually be registered (like the auth test).
	requireRegistered := func(t *testing.T, path, method string) {
		t.Helper()
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, path, nil)
		mux.ServeHTTP(rr, req)
		if rr.Code == http.StatusNotFound {
			t.Errorf("route %s %s is not registered (404)", method, path)
		}
	}
	for _, r := range []struct{ path, method string }{
		{"/api/mcpdir", http.MethodGet},
		{"/api/mcpdir", http.MethodPost},
	} {
		requireRegistered(t, r.path, r.method)
	}

	// --- Anonymous requests must be rejected (401), not silently allowed ---
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rr := httptest.NewRecorder()
		var body *strings.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"value":"mcp_data"}`)
			req := httptest.NewRequest(method, "/api/mcpdir", body)
			req.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(rr, req)
		} else {
			req := httptest.NewRequest(method, "/api/mcpdir", nil)
			mux.ServeHTTP(rr, req)
		}
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous: expected 401, got %d (%s)", method, rr.Code, rr.Body.String())
		}
	}

	// Log in as admin to obtain a valid token.
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
	authHeader := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	}
	uid := user.ID

	// --- GET /api/mcpdir: initially empty ---
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/mcpdir", nil)
	authHeader(req)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/mcpdir (empty): status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp mcpdirResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET /api/mcpdir: %v", err)
	}
	if resp.Value != "" {
		t.Fatalf("GET /api/mcpdir (empty): expected empty value, got %q", resp.Value)
	}

	// --- POST /api/mcpdir with a valid value ---
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/mcpdir", strings.NewReader(`{"value":"mcp_data"}`))
	req.Header.Set("Content-Type", "application/json")
	authHeader(req)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/mcpdir (valid): status=%d body=%s", rr.Code, rr.Body.String())
	}
	resp = mcpdirResponse{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode POST /api/mcpdir: %v", err)
	}
	if resp.Value != "mcp_data" {
		t.Fatalf("POST /api/mcpdir (valid): expected value %q, got %q", "mcp_data", resp.Value)
	}

	// --- Verify the setting persisted in the DB for the user ---
	raw, err := d.Settings.Get(uid, "mcpWorkdir")
	if err != nil {
		t.Fatalf("Settings.Get: %v", err)
	}
	if raw != "mcp_data" {
		t.Fatalf("persisted mcpWorkdir: expected %q, got %q", "mcp_data", raw)
	}

	// --- GET again to confirm round-trip ---
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/mcpdir", nil)
	authHeader(req)
	mux.ServeHTTP(rr, req)
	resp = mcpdirResponse{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET /api/mcpdir (round-trip): %v", err)
	}
	if resp.Value != "mcp_data" {
		t.Fatalf("round-trip: expected %q, got %q", "mcp_data", resp.Value)
	}

	// --- POST /api/mcpdir with an invalid value (traversal) ---
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/mcpdir", strings.NewReader(`{"value":"../evil"}`))
	req.Header.Set("Content-Type", "application/json")
	authHeader(req)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/mcpdir (invalid): expected 400, got %d (%s)", rr.Code, rr.Body.String())
	}

	// --- POST /api/mcpdir with empty value clears the setting ---
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/mcpdir", strings.NewReader(`{"value":""}`))
	req.Header.Set("Content-Type", "application/json")
	authHeader(req)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/mcpdir (clear): status=%d body=%s", rr.Code, rr.Body.String())
	}
	resp = mcpdirResponse{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode POST /api/mcpdir (clear): %v", err)
	}
	if resp.Value != "" {
		t.Fatalf("POST /api/mcpdir (clear): expected empty value, got %q", resp.Value)
	}
	raw, _ = d.Settings.Get(uid, "mcpWorkdir")
	if raw != "" {
		t.Fatalf("cleared mcpWorkdir: expected empty, got %q", raw)
	}
}
