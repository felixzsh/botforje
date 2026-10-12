package stores_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

var (
	t0 = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	t1 = time.Date(2026, 9, 13, 10, 5, 0, 0, time.UTC)
	t2 = time.Date(2026, 9, 13, 10, 10, 0, 0, time.UTC)
)

func open(t *testing.T, path string) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func ensureContact(t *testing.T, db *sqlite.DB) *botflota.Peer {
	t.Helper()
	contact, _, err := db.Peers().RecordInbound(context.Background(), "support",
		waKey("5215500000000"), "Ana", t0)
	if err != nil {
		t.Fatalf("RecordMessage contact: %v", err)
	}
	return contact
}

// waChatTarget is the direct conversation the helpers below share: the same
// WhatsApp subject the seeded peer carries (ADR 0051).
var waChatTarget = botflota.ChatTarget{
	Provider: "whatsapp", Kind: botflota.ChatDirect, Thread: "5215500000000",
}

func ensureChat(t *testing.T, db *sqlite.DB) *botflota.Chat {
	t.Helper()
	contact := ensureContact(t, db)
	chat, err := db.Chats().Ensure(context.Background(), "support", "support-bot",
		waChatTarget, contact.ID, t0)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return chat
}

func testSession(chatID int64) *botflota.Session {
	return &botflota.Session{
		ChatID:       chatID,
		Node:         "purchase",
		VisitedNodes: []string{"menu", "purchase"},
		GraphContext: map[string]any{"customer_name": "Ana", "visit_count": float64(2)},
		NodeContext: map[string]any{
			"product": "widget",
			"lookup":  map[string]any{"status": float64(200)},
		},
		CreatedAt: t0,
		UpdatedAt: t1,
		ExpiresAt: t2,
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))

	if _, err := db.Chats().Ensure(ctx, "support", "support-bot", waChatTarget,
		999, t0); err == nil {
		t.Fatal("Ensure with unknown peer succeeded, want a foreign key error")
	}

	orphanChat := &botflota.Message{
		ChatID: 999, Target: waChatTarget, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "orphan"}, At: t1,
	}
	if err := db.Messages().Append(ctx, orphanChat); err == nil {
		t.Fatal("Append with unknown chat succeeded, want an error")
	}

	chat := ensureChat(t, db)
	orphanSession := &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, SessionID: 999,
		Direction: botflota.Inbound,
		Content:   botflota.TextContent{Body: "orphan"}, At: t1,
	}
	if err := db.Messages().Append(ctx, orphanSession); err == nil {
		t.Fatal("Append with unknown session succeeded, want a foreign key error")
	}
}
