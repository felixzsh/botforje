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

func ensureChat(t *testing.T, db *DB) *domain.Chat {
	t.Helper()
	chat, err := db.Chats().Ensure(context.Background(), "support", "support-bot",
		"5215500000000", t0)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return chat
}

func testSession(chatID int64) *domain.Session {
	return &domain.Session{
		ChatID:       chatID,
		Node:         "purchase",
		VisitedNodes: []string{"menu", "purchase"},
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

func TestChatEnsureIsGetOrCreate(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	first := ensureChat(t, db)
	if first.ID == 0 {
		t.Fatal("Ensure did not set an id")
	}
	again, err := db.Chats().Ensure(ctx, "support", "support-bot", "5215500000000", t2)
	if err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("Ensure created a second chat: %d != %d", again.ID, first.ID)
	}
	if !again.StartedAt.Equal(t0) {
		t.Fatalf("started_at = %v, want %v", again.StartedAt, t0)
	}
	if err := db.Chats().Touch(ctx, first.ID, t1); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, err := db.Chats().Ensure(ctx, "support", "support-bot", "5215500000000", t2)
	if err != nil {
		t.Fatalf("Ensure after touch: %v", err)
	}
	if !got.UpdatedAt.Equal(t1) {
		t.Fatalf("updated_at = %v, want %v", got.UpdatedAt, t1)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	want := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if want.ID == 0 {
		t.Fatal("Save did not set a session id")
	}
	got, err := db.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSessionResumeAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	chat := ensureChat(t, first)
	want := testSession(chat.ID)
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
	got, err := second.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest after restart: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resume mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSessionSaveUpdates(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	session := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	session.Node = "menu"
	session.VisitedNodes = []string{"menu"}
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := db.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Node != "menu" || !reflect.DeepEqual(got.VisitedNodes, []string{"menu"}) {
		t.Fatalf("update not persisted: %#v", got)
	}
}

func TestSessionRunsAreHistory(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	first := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, first); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, second); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := db.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.ID != second.ID || second.ID == first.ID {
		t.Fatalf("Latest = %d, want the newest run %d (older was %d)",
			got.ID, second.ID, first.ID)
	}
	if err := db.Sessions().Delete(ctx, chat.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Sessions().Latest(ctx, chat.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Latest after delete = %v, want ErrNotFound", err)
	}
}

func TestSessionNotFound(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Sessions().Latest(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Latest missing = %v, want ErrNotFound", err)
	}
}

func TestChatAndMessages(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	session := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("Save session: %v", err)
	}

	for _, body := range []string{"hello", "hi there"} {
		message := &domain.Message{
			ChatID:    chat.ID,
			SessionID: session.ID,
			Direction: domain.Inbound,
			Kind:      domain.KindText,
			Body:      body,
			At:        t1,
		}
		if err := db.Messages().Append(ctx, message); err != nil {
			t.Fatalf("Append(%q): %v", body, err)
		}
		if message.ID == 0 {
			t.Fatalf("Append(%q) did not set an id", body)
		}
	}

	got, err := db.Messages().List(ctx, chat.ID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Body != "hello" || got[1].Body != "hi there" {
		t.Fatalf("List = %#v", got)
	}
	if got[0].SessionID != session.ID {
		t.Fatalf("SessionID = %d, want %d", got[0].SessionID, session.ID)
	}
}

func TestMessageWithoutSession(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	message := &domain.Message{
		ChatID: chat.ID, Direction: domain.Inbound, Kind: domain.KindText,
		Body: "no session", At: t1,
	}
	if err := db.Messages().Append(ctx, message); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := db.Messages().List(ctx, chat.ID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != 0 {
		t.Fatalf("SessionID = %#v, want 0", got)
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

func TestAccountList(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, account := range []domain.Account{
		{Scope: "z", Bot: "later", JID: "2@s.whatsapp.net"},
		{Scope: "a", Bot: "first", JID: "1@s.whatsapp.net"},
	} {
		if err := db.Accounts().Put(ctx, &account); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	got, err := db.Accounts().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Bot != "first" || got[1].Bot != "later" {
		t.Fatalf("List = %#v, want stable scope/bot order", got)
	}
}

func TestPairingStatusRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Accounts().GetPairingStatus(ctx, "dev", "bot"); !errors.Is(
		err, domain.ErrNotFound,
	) {
		t.Fatalf("GetPairingStatus missing = %v, want ErrNotFound", err)
	}
	want := &domain.PairingStatus{
		Scope: "dev", Bot: "bot", State: domain.PairingUnpaired, LastChange: t1,
	}
	if err := db.Accounts().SetPairingStatus(ctx, want); err != nil {
		t.Fatalf("SetPairingStatus: %v", err)
	}
	got, err := db.Accounts().GetPairingStatus(ctx, "dev", "bot")
	if err != nil {
		t.Fatalf("GetPairingStatus: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PairingStatus = %#v, want %#v", got, want)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))

	orphanChat := &domain.Message{
		ChatID: 999, Direction: domain.Inbound, Kind: domain.KindText,
		Body: "orphan", At: t1,
	}
	if err := db.Messages().Append(ctx, orphanChat); err == nil {
		t.Fatal("Append with unknown chat succeeded, want a foreign key error")
	}

	chat := ensureChat(t, db)
	orphanSession := &domain.Message{
		ChatID: chat.ID, SessionID: 999, Direction: domain.Inbound,
		Kind: domain.KindText, Body: "orphan", At: t1,
	}
	if err := db.Messages().Append(ctx, orphanSession); err == nil {
		t.Fatal("Append with unknown session succeeded, want a foreign key error")
	}
}
