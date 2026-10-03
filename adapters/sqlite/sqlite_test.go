package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

var (
	t0 = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	t1 = time.Date(2026, 9, 13, 10, 5, 0, 0, time.UTC)
)

func open(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPragmas(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestSchemaIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
}

// TestOpenAddsContentColumn covers the pre-release convergence: a database
// created before the content column existed gains it on open (ADR 0006).
func TestOpenAddsContentColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	_, err = legacy.Exec(`CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id INTEGER NOT NULL,
		session_id INTEGER,
		direction TEXT NOT NULL,
		kind TEXT NOT NULL,
		body TEXT NOT NULL,
		sent_at TEXT NOT NULL,
		provider_message_id TEXT NOT NULL DEFAULT '',
		ingest_kind TEXT NOT NULL DEFAULT 'unknown',
		origin TEXT NOT NULL DEFAULT '',
		routing TEXT NOT NULL DEFAULT 'unknown'
	)`)
	if err != nil {
		t.Fatalf("create legacy messages: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy: %v", err)
	}

	db := open(t, path)
	var columns int
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('messages')
		WHERE name = 'content'`).Scan(&columns)
	if err != nil {
		t.Fatalf("inspect columns: %v", err)
	}
	if columns != 1 {
		t.Fatalf("content column count = %d, want 1", columns)
	}
	// A legacy row decodes from its kind and visible text.
	contact, _, err := db.Contacts().RecordInbound(context.Background(), "support",
		"5215500000000", "Ana", t0)
	if err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	chat, err := db.Chats().Ensure(context.Background(), "support",
		"support-bot", contact.ID, t0)
	if err != nil {
		t.Fatalf("seed chat: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO messages
		(chat_id, direction, kind, body, sent_at) VALUES (?, 'in', 'text', 'old', ?)`,
		chat.ID, t1.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	got, err := db.Messages().History(context.Background(), chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 1 || botflota.ContentText(got[0].Content) != "old" {
		t.Fatalf("legacy row = %#v, want the stored text", got)
	}
}
