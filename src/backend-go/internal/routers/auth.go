package routers

import (
	"net/http"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// authUser mirrors the Python auth demo user dict shape.
type authUser struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Password string `json:"-"` // bcrypt hash; never serialized
	Email    string `json:"-"` // not serialized to keep the {user_id,username,email} response
}

type authHandler struct {
	mu         sync.Mutex
	users      []authUser
	nextUserID int
}

func newAuthHandler() *authHandler {
	return &authHandler{users: []authUser{}, nextUserID: 1}
}

func (h *authHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, u := range h.users {
		if u.Username == body.Username {
			writeError(w, http.StatusConflict, "username already registered")
			return
		}
	}

	id := h.nextUserID
	h.nextUserID++
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	u := authUser{
		UserID:   "USR-" + strconv.Itoa(id),
		Username: body.Username,
		Password: string(hash),
		Email:    body.Email,
	}
	h.users = append(h.users, u)

	writeJSON(w, http.StatusOK, map[string]string{
		"user_id":  u.UserID,
		"username": u.Username,
		"email":    u.Email,
	})
}

func (h *authHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, u := range h.users {
		if u.Username == body.Username {
			if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(body.Password)) == nil {
				// Demo posture: unsigned token, per the plan.
				writeJSON(w, http.StatusOK, map[string]string{
					"access_token": "demo-token-" + u.UserID,
					"token_type":   "bearer",
				})
				return
			}
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
	}
	writeError(w, http.StatusUnauthorized, "invalid credentials")
}

func (h *authHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.users) == 0 {
		writeError(w, http.StatusNotFound, "no users registered")
		return
	}
	first := h.users[0]
	writeJSON(w, http.StatusOK, map[string]string{
		"user_id":  first.UserID,
		"username": first.Username,
		"email":    first.Email,
	})
}

var _ = strings.TrimSpace
