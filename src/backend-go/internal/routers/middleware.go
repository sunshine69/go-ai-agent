// Package routers — middleware.go: shared helpers for authenticated requests.
//
// Authenticated routes wrap their handlers with requireAuth(w, r, inner). The
// middleware extracts an "Authorization: Bearer <jwt>" header, verifies the
// token, and (on success) makes the integer user id available via
// currentUserID(r). Handlers that are owner-scoped (e.g. per-user conversations)
// then read that id to scope their queries.
package routers

import (
	"net/http"
	"strings"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
)

// bearerToken extracts the token from a Bearer Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(h[len("Bearer "):])
}

// currentUserID verifies the bearer token and returns (userID, true) on success.
func currentUserID(r *http.Request) (int64, bool) {
	token := bearerToken(r)
	if token == "" {
		return 0, false
	}
	uid, err := db.ParseToken(token)
	if err != nil {
		return 0, false
	}
	return uid, true
}

// currentUserIDOr is like currentUserID but returns the fallback when the token
// is missing/invalid.
func currentUserIDOr(r *http.Request, fallback int64) int64 {
	if uid, ok := currentUserID(r); ok {
		return uid
	}
	return fallback
}

// requireAuth returns a handler that guards inner: only a caller presenting a
// valid "Authorization: Bearer <jwt>" token may proceed. It is the mux-level
// counterpart to the per-handler currentUserID checks and lets ServeMux protect
// an endpoint in one place. On failure it writes a 401 with the standard
// error body and does not call inner. This protects the "brain" and read
// endpoints (/api/messages*, /api/domains) that would otherwise let an
// anonymous caller resolve user id 0 against the datastore.
func requireAuth(inner http.HandlerFunc) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := currentUserID(r); !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		inner(w, r)
	}
}

// requireAdmin checks that the caller is authenticated and returns true. It is
// meant to be used together with the auth handler having a db reference:
//
//	if !requireAdmin(r, h.db, w) { return }
//
// where admin enforcement is decided by the caller using the resolved user.
func requireAdmin(r *http.Request, h *authHandler, w http.ResponseWriter) bool {
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "admin access required")
		return false
	}
	u, err := h.db.Users.GetByID(uid)
	if err != nil {
		writeError(w, http.StatusForbidden, "admin access required")
		return false
	}
	if !u.IsAdmin {
		writeError(w, http.StatusForbidden, "admin privileges required")
		return false
	}
	return true
}
