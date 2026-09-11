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
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// DB bundles the underlying connection pool with the repositories that use it.
type DB struct {
	db *sql.DB

	Users       *UserRepo
	Conversations *ConversationRepo
	Settings      *SettingsRepo
	driverName string

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
	d.Settings = newSettingsRepo(d)
	d.driverName = driverName

	if err := d.migrate(); err != nil {
		database.Close()
		return nil, err
	}
	if err := d.migrateSchema(); err != nil {
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

    CREATE TABLE IF NOT EXISTS user_settings (
        user_id INTEGER NOT NULL,
        key     TEXT NOT NULL,
        value   TEXT NOT NULL,
        PRIMARY KEY (user_id, key)
    );

    CREATE INDEX IF NOT EXISTS idx_user_settings_user ON user_settings(user_id);

    CREATE TABLE IF NOT EXISTS conversations (
        id          INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id     INTEGER NOT NULL,
        title       TEXT NOT NULL DEFAULT 'Untitled',
        created_at  TEXT NOT NULL DEFAULT (datetime('now')),
        updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
    );

    CREATE TABLE IF NOT EXISTS messages (
        id              INTEGER PRIMARY KEY AUTOINCREMENT,
        conversation_id INTEGER NOT NULL,
        tool_calls      TEXT NOT NULL DEFAULT '[]',
        tool_call_id    TEXT NOT NULL DEFAULT '',
        role            TEXT NOT NULL,
        content         TEXT NOT NULL,
        key             TEXT NOT NULL DEFAULT '',
        sources         TEXT NOT NULL DEFAULT '[]',
        confluence      TEXT NOT NULL DEFAULT '[]',
        created_at      TEXT NOT NULL DEFAULT (datetime('now'))
    );

    CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations(user_id);
    CREATE INDEX IF NOT EXISTS idx_messages_conv      ON messages(conversation_id);`
	if _, err := d.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// migrateSchema repairs a stale schema left behind by an older build.
//
// migrate() above only ever runs "CREATE TABLE IF NOT EXISTS", so it can add
// brand-new tables but can never fix a table that already exists on disk with a
// stale definition. The code in this repo (see conversation_repo.go) depends on
// the messages table having tool_calls and tool_call_id columns. If the
// database file predates the code that needs those columns, those columns are
// missing, which turns every INSERT/SELECT into a silent failure and makes
// conversations appear empty — exactly the "back and forth" bug reported for
// the "MRI test" search.
//
// migrateSchema runs on every startup (it is fully idempotent) and adds any
// column that the current code depends on but that is absent from the live
// table. It does NOT drop, rename, or rewrite anything, and it never removes a
// column, so it is safe to run repeatedly.
func (d *DB) migrateSchema() error {
	// A non-sqlite driver (e.g. postgres) has its own migration story and the
	// repo does not use driver-specific column semantics here.
	if d.driverName != "sqlite3" {
		return nil
	}

	// ensureColumn adds a column if it does not already exist. It is a no-op
	// when the column is already present, so it is safe to call on every start.
	//
	// "ALTER ... ADD COLUMN" requires a non-NULL value unless a default is
	// supplied; every column below carries a default so existing rows are
	// back-filled safely.
	ensureColumn := func(table, column, def string) error {
		exists, err := d.columnExists(table, column)
		if err != nil {
			return fmt.Errorf("check %s.%s: %w", table, column, err)
		}
		if exists {
			return nil
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def)
		if _, err := d.db.Exec(stmt); err != nil {
			return fmt.Errorf("add column %s.%s: %w", table, column, err)
		}
		log.Printf("[DB] migrated: added column %s.%s to repair stale schema", table, column)
		return nil
	}

	// The messages table is the one that historically fell out of sync with the
	// code (missing tool_calls / tool_call_id columns). Keep this list in sync
	// with the columns conversation_repo.go reads and writes.
	migrations := []struct {
		table, column, def string
	}{
		{"messages", "tool_calls", `TEXT NOT NULL DEFAULT '[]'`},
		{"messages", "tool_call_id", `TEXT NOT NULL DEFAULT ''`},
	}

	for _, m := range migrations {
		if err := ensureColumn(m.table, m.column, m.def); err != nil {
			return err
		}
	}
	return nil
}

// columnExists reports whether column exists in table for a SQLite database.
// It returns false if the table does not exist.
func (d *DB) columnExists(table, column string) (bool, error) {
	rows, err := d.db.QueryContext(context.Background(),
		`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
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
