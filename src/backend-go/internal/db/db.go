// Package db provides the database layer for the backend. It exposes a generic
// data-access API backed by Go's standard `database/sql` package so the same
// code can run on SQLite (default, used for the built-in desktop/server build)
// or PostgreSQL (swap the driver name in OpenDB) with no changes elsewhere.
//
// The two repositories exposed here are:
//
//   - *UserRepo      — user accounts, password hashes, JWT tokens, login times.
//   - *ConversationRepo — per-user chat conversation history.
//
// A *DB bundles the raw *sql.DB together with the repositories that depend on
// it. Construct one through New() which applies the schema and (when
// seedAdmin is true) seeds the initial admin account.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// DB bundles the underlying connection pool with the repositories that use it.
type DB struct {
	db *sql.DB

	Users       *UserRepo
	Conversations *ConversationRepo

	once sync.Once
}

// Open opens (creating if necessary) the database at path and returns a ready
// *DB. driverName is "sqlite3" (default) or "postgres". The postgres driver is
// NOT compiled in by default; provide a registered driver name only when that
// driver is available to the build.
func Open(path, driverName string) (*DB, error) {
	if driverName == "" {
		driverName = "sqlite3"
	}
	database, err := sql.Open(driverName, path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}

	if driverName == "sqlite3" {
		// SQLite is a file-backed store; serialise writes to avoid the
		// "database is locked" error under concurrent requests.
		database.SetMaxOpenConns(1)
	} else {
		database.SetMaxOpenConns(25)
	}

	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping %q: %w", path, err)
	}

	d := &DB{db: database}
	d.Users = newUserRepo(d)
	d.Conversations = newConversationRepo(d)

	if err := d.migrate(); err != nil {
		database.Close()
		return nil, err
	}

	return d, nil
}

// migrate creates the schema on first use. It is idempotent (CREATE TABLE IF
// NOT EXISTS) so it is safe to run on every process start.
func (d *DB) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    login_name    TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    last_login    TEXT
);

CREATE TABLE IF NOT EXISTS users_token (
    user_id    INTEGER NOT NULL,
    token      TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, token)
);

CREATE TABLE IF NOT EXISTS conversations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    title       TEXT NOT NULL DEFAULT 'Untitled',
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS messages (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL,
    role           TEXT NOT NULL,
    content        TEXT NOT NULL,
    key            TEXT NOT NULL DEFAULT '',
    sources        TEXT NOT NULL DEFAULT '[]',
    confluence     TEXT NOT NULL DEFAULT '[]',
    created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations(user_id);
CREATE INDEX IF NOT EXISTS idx_messages_conv      ON messages(conversation_id);
`
	if _, err := d.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// SeedAdmin seeds an admin user with the given password if no admin exists yet.
// Returns the created user or an error.
func (d *DB) SeedAdmin(login, email, password string) (*User, error) {
	return d.Users.seedAdmin(login, email, password)
}

// Close releases the underlying database resources.
func (d *DB) Close() error {
	return d.db.Close()
}

// ErrNotFound is returned by repository lookups when no row matches.
var ErrNotFound = errors.New("not found")
