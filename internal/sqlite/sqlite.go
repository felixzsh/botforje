// Package sqlite implements the domain repositories on SQLite. SQL stays in
// this adapter; the domain owns the interfaces (ADR 0006).
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// schema is the whole database, applied on open with IF NOT EXISTS. Pre-release
// it is edited in place and the development database is recreated with
// `make dev-reset`; versioned, append-only migrations start at the first
// release (ADR 0006).
var schema = []string{
	`CREATE TABLE IF NOT EXISTS conversations (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		bot        TEXT NOT NULL,
		sender     TEXT NOT NULL,
		scope      TEXT NOT NULL,
		started_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS conversations_bot_sender ON conversations (bot, sender)`,
	`CREATE TABLE IF NOT EXISTS messages (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id INTEGER NOT NULL
			REFERENCES conversations (id) ON DELETE CASCADE,
		direction       TEXT NOT NULL,
		kind            TEXT NOT NULL,
		body            TEXT NOT NULL,
		sent_at         TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS messages_conversation ON messages (conversation_id, id)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		bot           TEXT NOT NULL,
		sender        TEXT NOT NULL,
		scope         TEXT NOT NULL,
		node          TEXT NOT NULL,
		memory        TEXT NOT NULL,
		graph_context TEXT NOT NULL,
		node_context  TEXT NOT NULL,
		cooldowns     TEXT NOT NULL,
		created_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL,
		expires_at    TEXT NOT NULL,
		PRIMARY KEY (scope, bot, sender)
	)`,
	`CREATE TABLE IF NOT EXISTS accounts (
		scope TEXT NOT NULL,
		bot   TEXT NOT NULL,
		jid   TEXT NOT NULL,
		PRIMARY KEY (scope, bot)
	)`,
	`CREATE TABLE IF NOT EXISTS pairing_status (
		scope       TEXT NOT NULL,
		bot         TEXT NOT NULL,
		state       TEXT NOT NULL,
		last_change TEXT NOT NULL,
		PRIMARY KEY (scope, bot)
	)`,
}

// DB is an open SQLite database with its schema applied.
type DB struct{ *sql.DB }

// Open opens (creating if needed) the database at path, enables the pragmas
// from ADR 0006, and applies the schema.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("sqlite: %w", err)
		}
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}
	if err := applySchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{DB: db}, nil
}

// Sessions returns the session repository.
func (d *DB) Sessions() *SessionStore { return &SessionStore{db: d.DB} }

// Conversations returns the conversation repository.
func (d *DB) Conversations() *ConversationStore {
	return &ConversationStore{db: d.DB}
}

// Messages returns the message repository.
func (d *DB) Messages() *MessageStore { return &MessageStore{db: d.DB} }

// Accounts returns the paired-account repository.
func (d *DB) Accounts() *AccountStore { return &AccountStore{db: d.DB} }

func applySchema(db *sql.DB) error {
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("sqlite: schema: %w", err)
		}
	}
	return nil
}
