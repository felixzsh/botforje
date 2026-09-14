package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botforje/internal/domain"
)

var (
	t0 = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	t1 = time.Date(2026, 9, 13, 10, 5, 0, 0, time.UTC)
	t2 = time.Date(2026, 9, 13, 10, 10, 0, 0, time.UTC)
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

func testSession() *domain.Session {
	return &domain.Session{
		Bot:          "support-bot",
		Sender:       "5215500000000",
		SenderLID:    "212150010273830",
		Scope:        "support",
		Node:         "purchase",
		Memory:       []string{"menu", "purchase"},
		GraphContext: map[string]any{"customer_name": "Ana", "visit_count": float64(2)},
		NodeContext: map[string]any{
			"product": "widget",
			"lookup":  map[string]any{"status": float64(200)},
		},
		Cooldowns: map[string]time.Time{"escalate": t1},
		CreatedAt: t0,
		UpdatedAt: t1,
		ExpiresAt: t2,
	}
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

func TestMigrationsIdempotent(t *testing.T) {
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

func TestSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	want := testSession()
	if err := db.Sessions().Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := db.Sessions().Get(ctx, want.Scope, want.Bot, want.Sender)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSessionResumeAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	want := testSession()

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Sessions().Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
	got, err := second.Sessions().Get(ctx, want.Scope, want.Bot, want.Sender)
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resume mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSessionSaveUpdates(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	session := testSession()
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	session.Node = "menu"
	session.Memory = []string{"menu"}
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := db.Sessions().Get(ctx, session.Scope, session.Bot, session.Sender)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Node != "menu" || !reflect.DeepEqual(got.Memory, []string{"menu"}) {
		t.Fatalf("update not persisted: %#v", got)
	}
}

func TestSessionNotFoundAndDelete(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Sessions().Get(ctx, "default", "b", "s"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	session := testSession()
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := db.Sessions().Delete(ctx, session.Scope, session.Bot, session.Sender); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Sessions().Get(ctx, session.Scope, session.Bot, session.Sender); !errors.Is(
		err, domain.ErrNotFound,
	) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestConversationAndMessages(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))

	conversation := &domain.Conversation{
		Bot: "support-bot", Sender: "5215500000000", Scope: "support",
		StartedAt: t0, UpdatedAt: t0,
	}
	if err := db.Conversations().Start(ctx, conversation); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if conversation.ID == 0 {
		t.Fatal("Start did not set an id")
	}
	if err := db.Conversations().Touch(ctx, conversation.ID, t1); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	for _, body := range []string{"hello", "hi there"} {
		message := &domain.Message{
			ConversationID: conversation.ID,
			Direction:      domain.Inbound,
			Kind:           domain.KindText,
			Body:           body,
			At:             t1,
		}
		if err := db.Messages().Append(ctx, message); err != nil {
			t.Fatalf("Append(%q): %v", body, err)
		}
		if message.ID == 0 {
			t.Fatalf("Append(%q) did not set an id", body)
		}
	}

	got, err := db.Messages().List(ctx, conversation.ID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Body != "hello" || got[1].Body != "hi there" {
		t.Fatalf("List = %#v", got)
	}
}

func TestAccountRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Accounts().Get(ctx, "support", "support-bot"); !errors.Is(
		err, domain.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	account := &domain.Account{
		Scope: "support", Bot: "support-bot", JID: "5215500000000@s.whatsapp.net",
	}
	if err := db.Accounts().Put(ctx, account); err != nil {
		t.Fatalf("Put: %v", err)
	}
	account.JID = "5215599999999@s.whatsapp.net"
	if err := db.Accounts().Put(ctx, account); err != nil {
		t.Fatalf("Put update: %v", err)
	}
	got, err := db.Accounts().Get(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.JID != "5215599999999@s.whatsapp.net" {
		t.Fatalf("JID = %q", got.JID)
	}
	if err := db.Accounts().Delete(ctx, "support", "support-bot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Accounts().Get(ctx, "support", "support-bot"); !errors.Is(
		err, domain.ErrNotFound,
	) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	message := &domain.Message{
		ConversationID: 999, Direction: domain.Inbound, Kind: domain.KindText,
		Body: "orphan", At: t1,
	}
	if err := db.Messages().Append(ctx, message); err == nil {
		t.Fatal("Append with unknown conversation succeeded, want a foreign key error")
	}
}
