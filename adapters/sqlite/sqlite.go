// Package sqlite opens the SQLite database behind the store ports: pragmas,
// schema, and the repositories in stores (ADR 0006).
package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/felixzsh/botflota/adapters/sqlite/stores"

	_ "modernc.org/sqlite"
)

// schema is the whole database, applied on open with IF NOT EXISTS.
// Pre-release it is edited in place (`make dev-reset` recreates the dev
// database); versioned migrations start at the first release (ADR 0006).
// Shapes: ADR 0026, ADR 0027, ADR 0029, ADR 0051.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS peers (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		scope           TEXT NOT NULL,
		provider        TEXT NOT NULL,
		subject         TEXT NOT NULL,
		display_name    TEXT NOT NULL DEFAULT '',
		name_updated_at TEXT NOT NULL DEFAULT '',
		first_seen      TEXT NOT NULL DEFAULT '',
		last_seen       TEXT NOT NULL DEFAULT '',
		UNIQUE (scope, provider, subject)
	)`,
	`CREATE TABLE IF NOT EXISTS chats (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		scope         TEXT NOT NULL,
		bot           TEXT NOT NULL,
		provider      TEXT NOT NULL,
		kind          TEXT NOT NULL,
		thread        TEXT NOT NULL,
		topic         TEXT NOT NULL DEFAULT '',
		peer_id       INTEGER
			REFERENCES peers (id),
		started_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL,
		message_count INTEGER NOT NULL DEFAULT 0,
		guards        TEXT NOT NULL DEFAULT '{}',
		UNIQUE (scope, bot, provider, kind, thread, topic)
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
		scope               TEXT NOT NULL,
		bot                 TEXT NOT NULL,
		session_id          INTEGER
			REFERENCES sessions (id) ON DELETE SET NULL,
		target_provider     TEXT NOT NULL,
		target_kind         TEXT NOT NULL,
		target_thread       TEXT NOT NULL,
		target_topic        TEXT NOT NULL DEFAULT '',
		direction           TEXT NOT NULL,
		kind                TEXT NOT NULL,
		body                TEXT NOT NULL,
		content             TEXT NOT NULL DEFAULT '{}',
		sent_at             TEXT NOT NULL,
		provider_message_id TEXT NOT NULL DEFAULT '',
		message_space       TEXT NOT NULL DEFAULT '',
		ingest_kind         TEXT NOT NULL DEFAULT 'unknown',
		origin              TEXT NOT NULL DEFAULT '',
		routing             TEXT NOT NULL DEFAULT 'unknown'
	)`,
	`CREATE INDEX IF NOT EXISTS messages_chat ON messages (chat_id, id)`,
	// The durable dedup key is the scope, the bot, the adapter-declared
	// message space, the complete destination, and the provider id
	// (ADR 0026, ADR 0051): an alternate recipient's ids never collide
	// with the chat that records the send, one account's ids are never
	// taken for another's, and one bot's message is never taken for
	// another's. Binding generation is delivery authorization, never part
	// of this key; a moved message keeps the space it was created under.
	`CREATE UNIQUE INDEX IF NOT EXISTS messages_provider
		ON messages (scope, bot, message_space, target_provider,
		             target_kind, target_thread, target_topic,
		             provider_message_id)
		WHERE provider_message_id != ''`,
	`CREATE TABLE IF NOT EXISTS directory_entries (
		scope           TEXT NOT NULL,
		bot             TEXT NOT NULL,
		provider        TEXT NOT NULL,
		subject         TEXT NOT NULL,
		source_provider TEXT NOT NULL,
		source_subject  TEXT NOT NULL,
		display_name    TEXT NOT NULL DEFAULT '',
		updated_at      TEXT NOT NULL,
		PRIMARY KEY (scope, bot, provider, subject,
		             source_provider, source_subject)
	)`,
	`CREATE INDEX IF NOT EXISTS directory_entries_updated
		ON directory_entries (scope, bot, updated_at DESC)`,
	`CREATE TABLE IF NOT EXISTS bindings (
		scope      TEXT NOT NULL,
		bot        TEXT NOT NULL,
		provider   TEXT NOT NULL,
		adapter    TEXT NOT NULL DEFAULT '',
		mode       TEXT NOT NULL DEFAULT '',
		namespace  TEXT NOT NULL DEFAULT '',
		subject     TEXT NOT NULL,
		generation INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (scope, bot)
	)`,
	// binding_generations remembers the last generation committed per bot
	// after its binding row is gone, so a later association never reuses
	// an earlier generation (ADR 0053). Detach forgets the binding, never
	// this provenance.
	`CREATE TABLE IF NOT EXISTS binding_generations (
		scope      TEXT NOT NULL,
		bot        TEXT NOT NULL,
		generation INTEGER NOT NULL,
		updated_at TEXT NOT NULL,
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
	`CREATE TABLE IF NOT EXISTS outbox (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		scope           TEXT NOT NULL,
		bot             TEXT NOT NULL,
		message_id      INTEGER NOT NULL
			REFERENCES messages (id) ON DELETE CASCADE,
		target_provider TEXT NOT NULL,
		target_kind     TEXT NOT NULL,
		target_thread   TEXT NOT NULL,
		target_topic    TEXT NOT NULL DEFAULT '',
		client_id       TEXT NOT NULL DEFAULT '',
		binding_generation INTEGER NOT NULL DEFAULT 0,
		delivery_key    TEXT NOT NULL DEFAULT '',
		state           TEXT NOT NULL DEFAULT 'pending',
		attempts        INTEGER NOT NULL DEFAULT 0,
		next_attempt    TEXT NOT NULL DEFAULT '',
		error           TEXT NOT NULL DEFAULT '',
		created_at      TEXT NOT NULL,
		updated_at      TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS outbox_pending
		ON outbox (scope, bot, state, id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS outbox_client
		ON outbox (scope, bot, client_id) WHERE client_id != ''`,
	`CREATE TABLE IF NOT EXISTS webhook_deliveries (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id     TEXT NOT NULL,
		delivery_id  TEXT NOT NULL,
		url          TEXT NOT NULL,
		event        TEXT NOT NULL,
		scope        TEXT NOT NULL,
		payload      TEXT NOT NULL,
		state        TEXT NOT NULL DEFAULT 'pending',
		attempts     INTEGER NOT NULL DEFAULT 0,
		next_attempt TEXT NOT NULL DEFAULT '',
		error        TEXT NOT NULL DEFAULT '',
		created_at   TEXT NOT NULL,
		updated_at   TEXT NOT NULL,
		UNIQUE (event_id, url)
	)`,
	`CREATE INDEX IF NOT EXISTS webhook_pending
		ON webhook_deliveries (state, next_attempt, id)`,
}

const (
	// busyTimeoutMS is how long one statement waits for a held write lock
	// before returning SQLITE_BUSY. A turn that waits is still committed later;
	// failing at 5s under a busy fleet discarded work that fit, so the timeout
	// covers the queue that fleet produces instead of the disk's own latency.
	busyTimeoutMS = 30000
	// maxOpenConns bounds the process's SQLite connections. SQLite admits one
	// writer at a time, so extra connections add page caches (about a gigabyte
	// at 500 bots) and contention without adding write throughput; the Go pool
	// holds the queue instead.
	maxOpenConns = 8
)

// DB is an open SQLite database with its schema applied.
type DB struct{ *sql.DB }

// Open opens (creating if needed) the database at path with the ADR 0006
// pragmas and the schema.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("sqlite: %w", err)
		}
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(" + strconv.Itoa(busyTimeoutMS) + ")" +
		"&_pragma=synchronous(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)
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

func (d *DB) Chats() *stores.ChatStore { return stores.NewChatStore(d.DB) }

func (d *DB) Peers() *stores.PeerStore { return stores.NewPeerStore(d.DB) }

func (d *DB) Directory() *stores.DirectoryStore {
	return stores.NewDirectoryStore(d.DB)
}

func (d *DB) Sessions() *stores.SessionStore { return stores.NewSessionStore(d.DB) }

func (d *DB) Messages() *stores.MessageStore { return stores.NewMessageStore(d.DB) }

// History reads one bot's whole history in a single snapshot (ADR 0042).
func (d *DB) History() *stores.HistoryStore { return stores.NewHistoryStore(d.DB) }

// Turns returns the store that commits one message turn atomically.
func (d *DB) Turns() *stores.TurnStore { return stores.NewTurnStore(d.DB) }

// Outbox returns the durable delivery queue the turn writes into.
func (d *DB) Outbox() *stores.OutboxStore { return stores.NewOutboxStore(d.DB) }

// Sends commits an API-origin outbound message outside a turn (ADR 0044).
func (d *DB) Sends() *stores.SendStore { return stores.NewSendStore(d.DB) }

// Webhooks is the durable outbound webhook queue (ADR 0045).
func (d *DB) Webhooks() *stores.WebhookStore { return stores.NewWebhookStore(d.DB) }

func (d *DB) Bindings() *stores.BindingStore {
	return stores.NewBindingStore(d.DB)
}

func (d *DB) OwnerTokens() *stores.OwnerTokenStore {
	return stores.NewOwnerTokenStore(d.DB)
}

func (d *DB) AccessTokens() *stores.AccessTokenStore {
	return stores.NewAccessTokenStore(d.DB)
}

func applySchema(db *sql.DB) error {
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("sqlite: schema: %w", err)
		}
	}
	return nil
}
