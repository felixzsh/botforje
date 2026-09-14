// Package sqlite implements the domain repositories on SQLite. SQL stays in
// this adapter; the domain owns the interfaces (ADR 0006).
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// migrations are applied in order and recorded, so each runs once per database
// (ADR 0006). Never edit an applied migration; append a new one.
var migrations = []string{
	`CREATE TABLE conversations (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		bot        TEXT NOT NULL,
		sender     TEXT NOT NULL,
		scope      TEXT NOT NULL,
		started_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE INDEX conversations_bot_sender ON conversations (bot, sender)`,
	`CREATE TABLE messages (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id INTEGER NOT NULL
			REFERENCES conversations (id) ON DELETE CASCADE,
		direction       TEXT NOT NULL,
		kind            TEXT NOT NULL,
		body            TEXT NOT NULL,
		sent_at         TEXT NOT NULL
	)`,
	`CREATE INDEX messages_conversation ON messages (conversation_id, id)`,
	`CREATE TABLE sessions (
		bot           TEXT NOT NULL,
		sender        TEXT NOT NULL,
		sender_lid    TEXT NOT NULL DEFAULT '',
		scope         TEXT NOT NULL,
		node          TEXT NOT NULL,
		memory        TEXT NOT NULL,
		graph_context TEXT NOT NULL,
		node_context  TEXT NOT NULL,
		cooldowns     TEXT NOT NULL,
		created_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL,
		expires_at    TEXT NOT NULL,
		PRIMARY KEY (bot, sender)
	)`,
	`CREATE TABLE accounts (
		bot TEXT PRIMARY KEY,
		jid TEXT NOT NULL
	)`,
}

const migrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
)`

// DB is an open SQLite database with its schema migrated.
type DB struct{ *sql.DB }

// Open opens (creating if needed) the database at path, enables the pragmas
// from ADR 0006, and applies migrations.
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
	if err := migrate(db); err != nil {
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

func migrate(db *sql.DB) error {
	if _, err := db.Exec(migrationsTable); err != nil {
		return fmt.Errorf("sqlite: migrations table: %w", err)
	}
	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}
	for i, statement := range migrations {
		version := i + 1
		if applied[version] {
			continue
		}
		if err := applyMigration(db, version, statement); err != nil {
			return err
		}
	}
	return nil
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("sqlite: read migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

func applyMigration(db *sql.DB, version int, statement string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: begin migration %d: %w", version, err)
	}
	if _, err := tx.Exec(statement); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("sqlite: migration %d: %w", version, err)
	}
	_, err = tx.Exec(
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
		version, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("sqlite: record migration %d: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit migration %d: %w", version, err)
	}
	return nil
}
