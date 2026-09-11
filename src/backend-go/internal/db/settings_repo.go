package db

import (
	"context"
	"database/sql"
)

// SettingsValue is a single per-user setting row: a key/value pair. The value
// is always stored as text; callers decode it (strconv for ints, etc.) as
// needed.
type SettingsValue struct {
	UserID int64  `json:"user_id"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

// SettingsRepo is the data-access object for per-user application settings
// (user_settings table). Every key is owned by exactly one user; settings are
// therefore always user-scoped by the caller.
type SettingsRepo struct {
	db *DB
}

func newSettingsRepo(db *DB) *SettingsRepo {
	return &SettingsRepo{db: db}
}

// Get retrieves a user's setting value by key, returning ("", nil) when the
// key is not set.
func (sr *SettingsRepo) Get(userID int64, key string) (string, error) {
	var v string
	err := sr.db.db.QueryRowContext(context.Background(),
		"SELECT value FROM user_settings WHERE user_id = ? AND key = ?",
		userID, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// Set stores (inserting or replacing) a single setting for a user.
func (sr *SettingsRepo) Set(userID int64, key, value string) error {
	_, err := sr.db.db.ExecContext(context.Background(),
		`INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?)
		 ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`,
		userID, key, value)
	return err
}

// GetAll returns every setting key/value for a user.
func (sr *SettingsRepo) GetAll(userID int64) ([]SettingsValue, error) {
	rows, err := sr.db.db.QueryContext(context.Background(),
		"SELECT key, value FROM user_settings WHERE user_id = ? ORDER BY key", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SettingsValue
	for rows.Next() {
		var (
			key string
			v   string
		)
		if err := rows.Scan(&key, &v); err != nil {
			return nil, err
		}
		out = append(out, SettingsValue{UserID: userID, Key: key, Value: v})
	}
	return out, rows.Err()
}

// Delete removes a single setting key for a user. Returns ErrNotFound when the
// key was not present.
func (sr *SettingsRepo) Delete(userID int64, key string) error {
	res, err := sr.db.db.ExecContext(context.Background(),
		"DELETE FROM user_settings WHERE user_id = ? AND key = ?", userID, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetAll removes every setting for a user.
func (sr *SettingsRepo) ResetAll(userID int64) error {
	_, err := sr.db.db.ExecContext(context.Background(),
		"DELETE FROM user_settings WHERE user_id = ?", userID)
	return err
}
