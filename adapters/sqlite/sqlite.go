// Package sqlite implements the core store ports on SQLite. SQL stays in
// this adapter; the core package owns the interfaces (ADR 0006).
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
// release (ADR 0006). The chat, session, and message shapes are in ADR 0026;
// the chat's guard state is in ADR 0027; the sender profile is in ADR 0029.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS senders (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		scope           TEXT NOT NULL,
		phone           TEXT NOT NULL,
		display_name    TEXT NOT NULL DEFAULT '',
		name_updated_at TEXT NOT NULL DEFAULT '',
		first_seen      TEXT NOT NULL,
		last_seen       TEXT NOT NULL,
		UNIQUE (scope, phone)
	)`,
	`CREATE TABLE IF NOT EXISTS chats (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		scope         TEXT NOT NULL,
		bot           TEXT NOT NULL,
		sender_id     INTEGER NOT NULL
			REFERENCES senders (id),
		started_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL,
		message_count INTEGER NOT NULL DEFAULT 0,
		guards        TEXT NOT NULL DEFAULT '{}',
		UNIQUE (scope, bot, sender_id)
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id       INTEGER NOT NULL
			REFERENCES chats (id) ON DELETE CASCADE,
		node          TEXT NOT NULL,
		visited_nodes TEXT NOT NULL,
		graph_context TEXT NOT NULL,
		node_context  TEXT NOT NULL,
		created_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL,
		expires_at    TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS sessions_chat ON sessions (chat_id, id)`,
	`CREATE TABLE IF NOT EXISTS messages (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id             INTEGER NOT NULL
			REFERENCES chats (id) ON DELETE CASCADE,
		session_id          INTEGER
			REFERENCES sessions (id) ON DELETE SET NULL,
		direction           TEXT NOT NULL,
		kind                TEXT NOT NULL,
		body                TEXT NOT NULL,
		content             TEXT NOT NULL DEFAULT '{}',
		sent_at             TEXT NOT NULL,
		provider_message_id TEXT NOT NULL DEFAULT '',
		ingest_kind         TEXT NOT NULL DEFAULT 'unknown',
		origin              TEXT NOT NULL DEFAULT '',
		routing             TEXT NOT NULL DEFAULT 'unknown'
	)`,
	`CREATE INDEX IF NOT EXISTS messages_chat ON messages (chat_id, id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS messages_provider
		ON messages (chat_id, provider_message_id)
		WHERE provider_message_id != ''`,
	`CREATE TABLE IF NOT EXISTS contacts (
		scope         TEXT NOT NULL,
		bot           TEXT NOT NULL,
		phone         TEXT NOT NULL,
		name          TEXT NOT NULL DEFAULT '',
		push_name     TEXT NOT NULL DEFAULT '',
		business_name TEXT NOT NULL DEFAULT '',
		updated_at    TEXT NOT NULL,
		PRIMARY KEY (scope, bot, phone)
	)`,
	`CREATE INDEX IF NOT EXISTS contacts_updated
		ON contacts (scope, bot, updated_at DESC)`,
	`CREATE TABLE IF NOT EXISTS linked_accounts (
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
	`CREATE TABLE IF NOT EXISTS owner_token (
		id         INTEGER PRIMARY KEY CHECK (id = 1),
		hash       BLOB NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS access_tokens (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT NOT NULL,
		hash       BLOB NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL DEFAULT '',
		revoked_at TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS access_token_scopes (
		access_token_id INTEGER NOT NULL
			REFERENCES access_tokens (id) ON DELETE CASCADE,
		scope           TEXT NOT NULL,
		PRIMARY KEY (access_token_id, scope)
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

// Chats returns the chat repository.
func (d *DB) Chats() *ChatStore { return &ChatStore{db: d.DB} }

// Senders returns the sender-profile repository.
func (d *DB) Senders() *SenderStore { return &SenderStore{db: d.DB} }

// Contacts returns the imported address-book repository.
func (d *DB) Contacts() *ContactStore { return &ContactStore{db: d.DB} }

// Sessions returns the session repository.
func (d *DB) Sessions() *SessionStore { return &SessionStore{db: d.DB} }

// Messages returns the message repository.
func (d *DB) Messages() *MessageStore { return &MessageStore{db: d.DB} }

// LinkedAccounts returns the linked-account repository.
func (d *DB) LinkedAccounts() *LinkedAccountStore { return &LinkedAccountStore{db: d.DB} }

// Pairings returns the pairing-status repository.
func (d *DB) Pairings() *PairingStore { return &PairingStore{db: d.DB} }

// OwnerTokens returns the owner-token repository.
func (d *DB) OwnerTokens() *OwnerTokenStore { return &OwnerTokenStore{db: d.DB} }

// AccessTokens returns the scoped access-token repository.
func (d *DB) AccessTokens() *AccessTokenStore { return &AccessTokenStore{db: d.DB} }

func applySchema(db *sql.DB) error {
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("sqlite: schema: %w", err)
		}
	}
	// CREATE TABLE IF NOT EXISTS leaves an existing table untouched, so a
	// column a newer build added needs an explicit ALTER. This keeps a
	// pre-release database working without the versioned runner of ADR 0006,
	// which starts at the first release.
	if err := addColumn(db, "messages", "content", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return err
	}
	return nil
}

// addColumn adds a column a newer build introduced and does nothing when it is
// already present.
func addColumn(db *sql.DB, table, column, definition string) error {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("sqlite: inspect %s: %w", table, err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var (
			position   int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultVal,
			&primaryKey); err != nil {
			return fmt.Errorf("sqlite: inspect %s: %w", table, err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: inspect %s: %w", table, err)
	}
	if found {
		return nil
	}
	statement := "ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition
	if _, err := db.Exec(statement); err != nil {
		return fmt.Errorf("sqlite: add %s.%s: %w", table, column, err)
	}
	return nil
}
