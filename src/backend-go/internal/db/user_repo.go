package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User is a user account. The PasswordHash is stored server-side and is never
// returned by the public UserView().
type User struct {
	ID           int64      `json:"id"`
	LoginName    string     `json:"login_name"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	IsAdmin      bool       `json:"is_admin"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLogin    *time.Time `json:"last_login,omitempty"`
}

// UserView is the serialisable view of a user. It never contains the password
// hash or the JWT token.
type UserView struct {
	ID        int64  `json:"id"`
	LoginName string `json:"login_name"`
	Email     string `json:"email"`
	IsAdmin   bool   `json:"is_admin"`
	CreatedAt string `json:"created_at"`
	LastLogin string `json:"last_login,omitempty"`
}

// UserRepo is the data access object for user accounts and their auth tokens.
type UserRepo struct {
	db *DB
}

func newUserRepo(db *DB) *UserRepo {
	return &UserRepo{db: db}
}

// seedAdmin creates an admin user with the given credentials if no admin exists
// yet. If an admin already exists it returns that user without re-seeding.
func (ur *UserRepo) seedAdmin(login, email, password string) (*User, error) {
	login = strings.TrimSpace(login)
	if login == "" {
		return nil, errors.New("admin login name is required")
	}

	var isAdmin int
	row := ur.db.db.QueryRowContext(nil, "SELECT is_admin FROM users WHERE login_name = ?", login)
	if err := row.Scan(&isAdmin); err == nil {
		// Admin already exists; return it without re-hashing / re-seeding.
		u, err := ur.getByLogin(login)
		if err == nil {
			return u, nil
		}
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := ur.db.db.ExecContext(
		nil,
		"INSERT INTO users (login_name, email, password_hash, is_admin, created_at) VALUES (?, ?, ?, 1, ?)",
		login, email, string(hash), now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert admin: %w", err)
	}
	_, _ = res.LastInsertId()
	return ur.getByLogin(login)
}

// getByLogin returns a user by their login name.
func (ur *UserRepo) getByLogin(login string) (*User, error) {
	login = strings.TrimSpace(login)
	row := ur.db.db.QueryRowContext(nil, `
		SELECT id, login_name, email, password_hash, is_admin, created_at, last_login
		FROM users WHERE login_name = ?`, login)
	u, err := scanUser(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

// getByID returns a user by primary key.
func (ur *UserRepo) getByID(id int64) (*User, error) {
	row := ur.db.db.QueryRowContext(nil, `
		SELECT id, login_name, email, password_hash, is_admin, created_at, last_login
		FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

// list returns every user (including their hash, which is stripped in UserView).
func (ur *UserRepo) list() ([]*User, error) {
	rows, err := ur.db.db.QueryContext(nil, `
		SELECT id, login_name, email, password_hash, is_admin, created_at, last_login
		FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserViews returns the public serialisable view of all users.
func (ur *UserRepo) UserViews() ([]UserView, error) {
	users, err := ur.list()
	if err != nil {
		return nil, err
	}
	views := make([]UserView, 0, len(users))
	for _, u := range users {
		views = append(views, u.toView())
	}
	return views, nil
}

// Insert creates a new user with a bcrypt-hashed password.
func (ur *UserRepo) Insert(login, email, password string, isAdmin bool) (*User, error) {
	login = strings.TrimSpace(login)
	if login == "" || password == "" {
		return nil, errors.New("login_name and password are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	adminInt := 0
	if isAdmin {
		adminInt = 1
	}
	res, err := ur.db.db.ExecContext(nil,
		"INSERT INTO users (login_name, email, password_hash, is_admin, created_at) VALUES (?, ?, ?, ?, ?)",
		login, email, string(hash), adminInt, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, fmt.Errorf("user %q already exists", login)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return ur.getByID(id)
}

// SetPassword updates a user's password hash.
func (ur *UserRepo) SetPassword(id int64, password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	res, err := ur.db.db.ExecContext(nil, "UPDATE users SET password_hash = ?, last_login = NULL WHERE id = ?", string(hash), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetEmail updates a user's email address.
func (ur *UserRepo) SetEmail(id int64, email string) error {
	res, err := ur.db.db.ExecContext(nil, "UPDATE users SET email = ? WHERE id = ?", email, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a user and all of their conversation history.
func (ur *UserRepo) Delete(id int64) error {
	tx, err := ur.db.db.BeginTx(nil, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(nil, "DELETE FROM messages WHERE conversation_id IN (SELECT id FROM conversations WHERE user_id = ?)", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(nil, "DELETE FROM conversations WHERE user_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(nil, "DELETE FROM users_token WHERE user_id = ?", id); err != nil {
		return err
	}
	res, err := tx.ExecContext(nil, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// VerifyPassword reports whether the supplied password matches the stored hash.
func (ur *UserRepo) VerifyPassword(login, password string) (*User, error) {
	u, err := ur.getByLogin(login)
	if err != nil {
		return nil, ErrNotFound
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrNotFound
	}
	// Refresh last_login timestamp on successful login.
	_, _ = ur.db.db.ExecContext(nil, "UPDATE users SET last_login = ? WHERE id = ?", time.Now().UTC().Format(time.RFC3339), u.ID)
	return u, nil
}

// HasUser reports whether a user with the given login exists (used by the
// frontend to display an informative message).
func (ur *UserRepo) HasUser(login string) bool {
	_, err := ur.getByLogin(login)
	return err == nil
}

// TokenFor stores (or returns an existing) JWT token bound to a user.
func (ur *UserRepo) TokenFor(userID int64, token string) error {
	_, err := ur.db.db.ExecContext(nil,
		"INSERT INTO users_token (user_id, token) VALUES (?, ?) ON CONFLICT(user_id, token) DO NOTHING",
		userID, token)
	return err
}

// DeleteToken removes a single JWT token for a user (used on logout).
func (ur *UserRepo) DeleteToken(userID int64, token string) error {
	_, err := ur.db.db.ExecContext(nil, "DELETE FROM users_token WHERE user_id = ? AND token = ?", userID, token)
	return err
}

// RevokeAll revokes every token for a user.
func (ur *UserRepo) RevokeAll(userID int64) error {
	_, err := ur.db.db.ExecContext(nil, "DELETE FROM users_token WHERE user_id = ?", userID)
	return err
}

// isValidToken reports whether the (user, token) pair is a known, valid token.
func (ur *UserRepo) isValidToken(userID int64, token string) bool {
	var n int
	row := ur.db.db.QueryRowContext(nil, "SELECT COUNT(1) FROM users_token WHERE user_id = ? AND token = ?", userID, token)
	if err := row.Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// --- helpers ---

// scanner is the subset of a rows/row interface that scanUser needs.
type scanner interface {
	Scan(dest ...any) error
}

func scanUser(s scanner) (*User, error) {
	var (
		u         User
		isAdmin   int
		createdAt string
		lastLogin sql.NullString
	)
	if err := s.Scan(&u.ID, &u.LoginName, &u.Email, &u.PasswordHash, &isAdmin, &createdAt, &lastLogin); err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin == 1
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		u.CreatedAt = t
	}
	if lastLogin.Valid {
		if t, err := time.Parse(time.RFC3339, lastLogin.String); err == nil {
			u.LastLogin = &t
		}
	}
	return &u, nil
}

func (u *User) toView() UserView {
	v := UserView{
		ID:        u.ID,
		LoginName: u.LoginName,
		Email:     u.Email,
		IsAdmin:   u.IsAdmin,
	}
	if !u.CreatedAt.IsZero() {
		v.CreatedAt = u.CreatedAt.UTC().Format(time.RFC3339)
	}
	if u.LastLogin != nil && !u.LastLogin.IsZero() {
		t := u.LastLogin.UTC().Format(time.RFC3339)
		v.LastLogin = t
	}
	return v
}
