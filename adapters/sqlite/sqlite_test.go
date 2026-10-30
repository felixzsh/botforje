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

// TestOpenCreatesNeutralIdentitySchema covers the pre-release reset policy:
// the database is created with the neutral identity and destination columns,
// and the provider id is unique per destination rather than per history chat
// (ADR 0006, ADR 0051).
func TestOpenCreatesNeutralIdentitySchema(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, column := range []struct{ table, name string }{
		{"peers", "subject"},
		{"chats", "thread"},
		{"chats", "topic"},
		{"messages", "target_thread"},
		{"directory_entries", "source_subject"},
		{"outbox", "target_thread"},
		{"bindings", "subject"},
	} {
		var found int
		err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`,
			column.table, column.name).Scan(&found)
		if err != nil {
			t.Fatalf("inspect %s: %v", column.table, err)
		}
		if found != 1 {
			t.Fatalf("%s.%s count = %d, want 1", column.table, column.name, found)
		}
	}
	var index string
	err := db.QueryRow(`SELECT sql FROM sqlite_master
		WHERE type = 'index' AND name = 'messages_provider'`).Scan(&index)
	if err != nil {
		t.Fatalf("inspect provider index: %v", err)
	}
	if !strings.Contains(index, "target_thread") || strings.Contains(index, "chat_id") {
		t.Fatalf("provider index = %q, want the destination scoping", index)
	}
	// The destination key separates two conversations that share one provider
	// id, which is what the chat-scoped index could not do.
	peer, _, err := db.Peers().RecordInbound(context.Background(), "support",
		botflota.IdentityKey{Provider: "whatsapp", Subject: "5215500000000"}, "Ana", t0)
	if err != nil {
		t.Fatalf("seed peer: %v", err)
	}
	target := botflota.ChatTarget{
		Provider: "whatsapp", Kind: botflota.ChatDirect, Thread: "5215500000000",
	}
	chat, err := db.Chats().Ensure(context.Background(), "support",
		"support-bot", target, peer.ID, t0)
	if err != nil {
		t.Fatalf("seed chat: %v", err)
	}
	for i, thread := range []string{"5215500000000", "5215500000001"} {
		message := &botflota.Message{
			ChatID: chat.ID, Direction: botflota.Inbound,
			Target: botflota.ChatTarget{
				Provider: "whatsapp", Kind: botflota.ChatDirect, Thread: thread,
			},
			Content: botflota.TextContent{Body: "old"}, At: t1,
			ProviderMessageID: "shared-id",
		}
		if err := db.Messages().Append(context.Background(), message); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	got, err := db.Messages().History(context.Background(), chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 || botflota.ContentText(got[0].Content) != "old" {
		t.Fatalf("rows = %#v, want both destinations", got)
	}
}

// TestConnectionPolicy covers the two runtime bounds the load measurements
// required (odd/tasks/sqlite-connection-policy.md): a turn waits for the write
// lock instead of failing after 5s, and the process never opens more SQLite
// connections than it can use, since each one carries its own page cache.
// PRAGMAs are per connection, so the timeout is checked on every connection the
// pool opens, not only on the first.
func TestConnectionPolicy(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	ctx := context.Background()
	conns := make([]*sql.Conn, 0, 8)
	for i := 0; i < 8; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
		conns = append(conns, conn)
	}
	for i, conn := range conns {
		var timeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatalf("busy_timeout on connection %d: %v", i, err)
		}
		if timeout != 30000 {
			t.Fatalf("connection %d busy_timeout = %d, want 30000", i, timeout)
		}
	}
	for i, conn := range conns {
		if err := conn.Close(); err != nil {
			t.Fatalf("close connection %d: %v", i, err)
		}
	}
	stats := db.Stats()
	if stats.MaxOpenConnections != 8 {
		t.Fatalf("MaxOpenConnections = %d, want 8", stats.MaxOpenConnections)
	}
	if stats.Idle != 8 {
		t.Fatalf("idle connections = %d, want the 8 kept warm", stats.Idle)
	}
}
