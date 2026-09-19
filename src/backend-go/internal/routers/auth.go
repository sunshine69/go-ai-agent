// Package routers — auth.go: database-backed, JWT-protected authentication.
//
// Endpoints (all served under /api):
//   - POST /api/auth/login       {username,password} -> {access_token, token_type, user}
//   - GET  /api/auth/me          (requires token)   -> {user}
//   - POST /api/auth/logout      (requires token)   -> ok
//   - GET  /api/auth/users       (requires admin)   -> [{id,login_name,email,is_admin,created_at,last_login}]
//   - POST /api/auth/users       (requires admin)   -> {user}
//   - DELETE /api/auth/users/{id} (requires admin)  -> ok
//   - GET  /api/auth/me/profile  (requires token)   -> {login_name,email,is_admin}
//   - PATCH /api/auth/me/profile (requires token)   -> {user}
//   - POST /api/auth/me/profile/password (requires token) -> ok
//
// The JWT access token is issued as an opaque HS256 bearer token whose subject
// is the user's integer id. Clients send it in the Authorization header as
// "Bearer <token>". Protected endpoints are wired in ServeMux via the
// requireAuth helper.
package routers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sunshine69/go-ai-agent/backend-go/internal/db"
)

// authUserView is the public user shape returned to clients (no password hash).
type authUserView struct {
	ID        int64  `json:"id"`
	LoginName string `json:"login_name"`
	Email     string `json:"email"`
	IsAdmin   bool   `json:"is_admin"`
	CreatedAt string `json:"created_at"`
	LastLogin string `json:"last_login,omitempty"`
}

// authHandler wraps the DB-backed auth endpoints and token issuance.
type authHandler struct {
	db *db.DB
}

func newAuthHandler(db *db.DB) *authHandler {
	return &authHandler{db: db}
}

// authLoginRequest is the body of POST /api/auth/login.
type authLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// authUserRequest is the body for creating a user (admin only).
type authUserRequest struct {
	Username string `json:"login_name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
	AllowMcp bool   `json:"allow_mcp"`
}

// authProfileUpdate is the body for updating email/username of the caller.
type authProfileUpdate struct {
	Email string `json:"email"`
}

// authPasswordChange is the body for changing the caller's password.
type authPasswordChange struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// handleLogin validates credentials and returns a signed access token.
func (h *authHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req authLoginRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	u, err := h.db.Users.VerifyPassword(username, req.Password)
	if err != nil {
		// Same message for all failures: avoid leaking which accounts exist.
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := db.IssueToken(u.ID, db.TokenExpiry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	if err := h.db.Users.TokenFor(u.ID, token); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "bearer",
		"user":         h.userView(u),
	})
}

// handleMe returns the caller's own user record.
func (h *authHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	u, err := h.db.Users.GetByID(uid)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, h.userView(u))
}

// handleLogout revokes the caller's token.
func (h *authHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	token := bearerToken(r)
	if token != "" {
		_ = h.db.Users.DeleteToken(uid, token)
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

// handleUsers lists all users (admin only).
func (h *authHandler) handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireAdmin(r, h, w) {
		return
	}
	views, err := h.db.Users.UserViews()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// handleCreateUser creates a new user (admin only).
// handleCreateUser creates a new user (admin only).
func (h *authHandler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireAdmin(r, h, w) {
		return
	}
	var req authUserRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "login_name and password are required")
		return
	}
	u, err := h.db.Users.Insert(req.Username, req.Email, req.Password, req.IsAdmin, req.AllowMcp)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, http.StatusOK, h.userView(u))
}

// handleRegister is disabled: users may only be created by an admin via
// POST /api/auth/users (admin-only). This endpoint returns 403 to any caller.
func (h *authHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusForbidden, "self-registration is disabled; ask an admin to create your account")
}

// (legacy) handleRegister previously created a new self-registered user and
// returned an access token for immediate login. It is now disabled (see above).
func (h *authHandler) handleRegisterDisabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req authUserRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "login_name and password are required")
		return
	}
	u, err := h.db.Users.Insert(username, strings.TrimSpace(req.Email), req.Password, false, false)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	token, err := db.IssueToken(u.ID, db.TokenExpiry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	if err := h.db.Users.TokenFor(u.ID, token); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store token")
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "bearer",
		"user":         h.userView(u),
	})
}
func (h *authHandler) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireAdmin(r, h, w) {
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/auth/users/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if id == currentUserIDOr(r, 0) {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	if err := h.db.Users.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete user")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "user deleted"})
}

// handleProfile gets the caller's profile.
func (h *authHandler) handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	u, err := h.db.Users.GetByID(uid)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"login_name": u.LoginName,
		"email":      u.Email,
		"is_admin":   u.IsAdmin,
	})
}

// handleProfileUpdate updates the caller's email/username.
func (h *authHandler) handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req authProfileUpdate
	_ = decodeBody(r, &req)
	if req.Email != "" {
		if err := h.db.Users.SetEmail(uid, req.Email); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update email")
			return
		}
	}
	u, _ := h.db.Users.GetByID(uid)
	writeJSON(w, http.StatusOK, h.userView(u))
}

// handlePasswordChange changes the caller's password after verifying the old one.
func (h *authHandler) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uid, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req authPasswordChange
	if err := decodeBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "old_password and new_password are required")
		return
	}
	// Re-verify the old password before allowing the change.
	cur, err := h.db.Users.GetByID(uid)
	if err != nil || cur == nil {
		writeError(w, http.StatusUnauthorized, "current user not found")
		return
	}
	u, err := h.db.Users.VerifyPassword(cur.LoginName, req.OldPassword)
	if err != nil || u == nil || u.ID != uid {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err := h.db.Users.SetPassword(uid, req.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "password updated"})
}

func (h *authHandler) userView(u *db.User) authUserView {
	v := authUserView{
		ID:        u.ID,
		LoginName: u.LoginName,
		Email:     u.Email,
		IsAdmin:   u.IsAdmin,
	}
	if !u.CreatedAt.IsZero() {
		v.CreatedAt = u.CreatedAt.UTC().Format(time.RFC3339)
	}
	if u.LastLogin != nil && !u.LastLogin.IsZero() {
		v.LastLogin = u.LastLogin.UTC().Format(time.RFC3339)
	}
	return v
}
