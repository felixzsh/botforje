package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
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

func ensureSender(t *testing.T, db *DB) *botflota.Sender {
	t.Helper()
	sender, _, err := db.Senders().Upsert(context.Background(), "support",
		"5215500000000", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert sender: %v", err)
	}
	return sender
}

func ensureChat(t *testing.T, db *DB) *botflota.Chat {
	t.Helper()
	sender := ensureSender(t, db)
	chat, err := db.Chats().Ensure(context.Background(), "support", "support-bot",
		sender.ID, t0)
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
	sender := ensureSender(t, db)
	first := ensureChat(t, db)
	if first.ID == 0 {
		t.Fatal("Ensure did not set an id")
	}
	again, err := db.Chats().Ensure(ctx, "support", "support-bot", sender.ID, t2)
	if err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("Ensure created a second chat: %d != %d", again.ID, first.ID)
	}
	if !again.StartedAt.Equal(t0) {
		t.Fatalf("started_at = %v, want %v", again.StartedAt, t0)
	}
	if err := db.Chats().RecordMessage(ctx, first.ID, t1); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	got, err := db.Chats().Ensure(ctx, "support", "support-bot", sender.ID, t2)
	if err != nil {
		t.Fatalf("Ensure after record: %v", err)
	}
	if !got.UpdatedAt.Equal(t1) {
		t.Fatalf("updated_at = %v, want %v", got.UpdatedAt, t1)
	}
	if got.MessageCount != 1 {
		t.Fatalf("message_count = %d, want 1", got.MessageCount)
	}
}

func TestChatGuardsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	if len(chat.Guards) != 0 {
		t.Fatalf("new chat guards = %#v, want empty", chat.Guards)
	}
	want := map[string]botflota.GuardState{
		"escalate": {
			Cooldown:  &botflota.CooldownState{LastRun: t1},
			RateLimit: &botflota.RateLimitState{WindowStart: t0, Count: 2},
		},
	}
	if err := db.Chats().SaveGuards(ctx, chat.ID, want); err != nil {
		t.Fatalf("SaveGuards: %v", err)
	}
	got, err := db.Chats().Ensure(ctx, "support", "support-bot", ensureSender(t, db).ID, t2)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !reflect.DeepEqual(got.Guards, want) {
		t.Fatalf("guards = %#v, want %#v", got.Guards, want)
	}
}

func TestChatGetAndList(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	sender := ensureSender(t, db)
	chat := ensureChat(t, db)
	got, err := db.Chats().ForSender(ctx, "support", "support-bot", sender.ID)
	if err != nil {
		t.Fatalf("ForSender: %v", err)
	}
	if got.ID != chat.ID {
		t.Fatalf("ForSender = %d, want %d", got.ID, chat.ID)
	}
	if _, err := db.Chats().ForSender(ctx, "support", "other", sender.ID); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("ForSender missing = %v, want ErrNotFound", err)
	}
	chats, err := db.Chats().Recent(ctx, "support", "", 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(chats) != 1 || chats[0].ID != chat.ID {
		t.Fatalf("Recent = %#v", chats)
	}
	if chats[0].Phone != "5215500000000" || chats[0].DisplayName != "Ana" {
		t.Fatalf("Recent sender profile = %#v", chats[0])
	}
	if byBot, err := db.Chats().Recent(ctx, "support", "support-bot", 10); err != nil ||
		len(byBot) != 1 {
		t.Fatalf("Recent by bot = %#v, %v", byBot, err)
	}
	if other, err := db.Chats().Recent(ctx, "support", "other", 10); err != nil ||
		len(other) != 0 {
		t.Fatalf("Recent other = %#v, %v", other, err)
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
	if _, err := db.Sessions().Latest(ctx, chat.ID); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("Latest after delete = %v, want ErrNotFound", err)
	}
}

func TestSessionNotFound(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Sessions().Latest(ctx, 999); !errors.Is(err, botflota.ErrNotFound) {
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
		message := &botflota.Message{
			ChatID:    chat.ID,
			SessionID: session.ID,
			Direction: botflota.Inbound,
			Kind:      botflota.KindText,
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

	got, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 || got[0].Body != "hello" || got[1].Body != "hi there" {
		t.Fatalf("History = %#v", got)
	}
	if got[0].SessionID != session.ID {
		t.Fatalf("SessionID = %d, want %d", got[0].SessionID, session.ID)
	}

	// A zero cursor keeps the newest messages; a cursor resumes from it.
	last, err := db.Messages().History(ctx, chat.ID, 0, 1)
	if err != nil {
		t.Fatalf("History(1): %v", err)
	}
	if len(last) != 1 || last[0].Body != "hi there" {
		t.Fatalf("History(1) = %#v, want the newest", last)
	}
	newer, err := db.Messages().History(ctx, chat.ID, got[0].ID, 10)
	if err != nil {
		t.Fatalf("History(after): %v", err)
	}
	if len(newer) != 1 || newer[0].ID != got[1].ID {
		t.Fatalf("History(after) = %#v, want only the newer message", newer)
	}
}

func TestMessageWithoutSession(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	message := &botflota.Message{
		ChatID: chat.ID, Direction: botflota.Inbound, Kind: botflota.KindText,
		Body: "no session", At: t1,
	}
	if err := db.Messages().Append(ctx, message); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != 0 {
		t.Fatalf("SessionID = %#v, want 0", got)
	}
}

// TestSessionCountActive covers the status metric: only unexpired runs of the
// bot count, and a run without expiry never expires.
func TestSessionCountActive(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	active := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, active); err != nil {
		t.Fatalf("Save active: %v", err)
	}
	never := testSession(chat.ID)
	never.ExpiresAt = time.Time{}
	if err := db.Sessions().Save(ctx, never); err != nil {
		t.Fatalf("Save never-expiring: %v", err)
	}
	expired := testSession(chat.ID)
	expired.ExpiresAt = t1.Add(-time.Hour)
	if err := db.Sessions().Save(ctx, expired); err != nil {
		t.Fatalf("Save expired: %v", err)
	}

	count, err := db.Sessions().CountActive(ctx, chat.Scope, chat.Bot, t1)
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if count != 2 {
		t.Fatalf("CountActive = %d, want 2 (expired excluded)", count)
	}
	if count, err = db.Sessions().CountActive(ctx, chat.Scope, "other", t1); err != nil {
		t.Fatalf("CountActive other: %v", err)
	} else if count != 0 {
		t.Fatalf("CountActive other = %d, want 0", count)
	}
}

func TestSenderUpsertIsGetOrCreate(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Senders().Get(ctx, "support", "52155"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	first, created, err := db.Senders().Upsert(ctx, "support", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !created {
		t.Fatal("Upsert did not report a new profile")
	}
	if first.ID == 0 || first.Phone != "52155" {
		t.Fatalf("profile = %#v", first)
	}
	if !first.FirstSeen.Equal(t0) || !first.LastSeen.Equal(t0) {
		t.Fatalf("first_seen = %v, last_seen = %v, want %v",
			first.FirstSeen, first.LastSeen, t0)
	}
}

func TestSenderUpsertUpdatesProfile(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	sender, _, err := db.Senders().Upsert(ctx, "support", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, created, err := db.Senders().Upsert(ctx, "support", "52155", "", t1)
	if err != nil {
		t.Fatalf("Upsert empty name: %v", err)
	}
	if created {
		t.Fatal("second Upsert reported a new profile")
	}
	if got.ID != sender.ID || !got.FirstSeen.Equal(t0) {
		t.Fatalf("profile = %#v, want id %d since %v", got, sender.ID, t0)
	}
	if got.DisplayName != "Ana" {
		t.Fatalf("display_name = %q, want the kept %q", got.DisplayName, "Ana")
	}
	if !got.LastSeen.Equal(t1) {
		t.Fatalf("last_seen = %v, want %v", got.LastSeen, t1)
	}
	if !got.NameUpdatedAt.Equal(t0) {
		t.Fatalf("name_updated_at = %v, want %v (unchanged name)", got.NameUpdatedAt, t0)
	}
	renamed, _, err := db.Senders().Upsert(ctx, "support", "52155", "Ana B", t2)
	if err != nil {
		t.Fatalf("Upsert new name: %v", err)
	}
	if renamed.DisplayName != "Ana B" || !renamed.NameUpdatedAt.Equal(t2) {
		t.Fatalf("rename = %#v, want Ana B stamped at %v", renamed, t2)
	}
}

func TestSenderIsScoped(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	first, _, err := db.Senders().Upsert(ctx, "a", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert a: %v", err)
	}
	second, _, err := db.Senders().Upsert(ctx, "b", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert b: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("one phone in two scopes must be two profiles")
	}
}

func TestOneSenderManyChats(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	sender := ensureSender(t, db)
	alpha, err := db.Chats().Ensure(ctx, "support", "alpha", sender.ID, t0)
	if err != nil {
		t.Fatalf("Ensure alpha: %v", err)
	}
	beta, err := db.Chats().Ensure(ctx, "support", "beta", sender.ID, t0)
	if err != nil {
		t.Fatalf("Ensure beta: %v", err)
	}
	if alpha.ID == beta.ID {
		t.Fatal("two bots must have two chats with the same sender")
	}
}

func TestLinkedAccountRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.LinkedAccounts().Get(ctx, "support", "support-bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	account := &botflota.LinkedAccount{
		Scope: "support", Bot: "support-bot", JID: "5215500000000@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatalf("Put: %v", err)
	}
	account.JID = "5215599999999@s.whatsapp.net"
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatalf("Put update: %v", err)
	}
	got, err := db.LinkedAccounts().Get(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.JID != "5215599999999@s.whatsapp.net" {
		t.Fatalf("JID = %q", got.JID)
	}
	if err := db.LinkedAccounts().Delete(ctx, "support", "support-bot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.LinkedAccounts().Get(ctx, "support", "support-bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestLinkedAccountList(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, account := range []botflota.LinkedAccount{
		{Scope: "z", Bot: "later", JID: "2@s.whatsapp.net"},
		{Scope: "a", Bot: "first", JID: "1@s.whatsapp.net"},
	} {
		if err := db.LinkedAccounts().Put(ctx, &account); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	got, err := db.LinkedAccounts().List(ctx)
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
	if _, err := db.Pairings().Get(ctx, "dev", "bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("pairing status missing = %v, want ErrNotFound", err)
	}
	want := &botflota.PairingStatus{
		Scope: "dev", Bot: "bot", State: botflota.PairingUnpaired, LastChange: t1,
	}
	if err := db.Pairings().Set(ctx, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := db.Pairings().Get(ctx, "dev", "bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PairingStatus = %#v, want %#v", got, want)
	}
}

func TestOwnerTokenRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.OwnerTokens().Get(ctx); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	want := &botflota.OwnerToken{Hash: []byte{1, 2, 3}, CreatedAt: t0}
	if err := db.OwnerTokens().Put(ctx, want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := db.OwnerTokens().Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OwnerToken = %#v, want %#v", got, want)
	}
	rotated := &botflota.OwnerToken{Hash: []byte{9}, CreatedAt: t1}
	if err := db.OwnerTokens().Put(ctx, rotated); err != nil {
		t.Fatalf("Put rotate: %v", err)
	}
	got, err = db.OwnerTokens().Get(ctx)
	if err != nil {
		t.Fatalf("Get after rotate: %v", err)
	}
	if !reflect.DeepEqual(got, rotated) {
		t.Fatalf("OwnerToken after rotate = %#v, want %#v", got, rotated)
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.AccessTokens().FindByHash(ctx, []byte("missing")); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("FindByHash missing = %v, want ErrNotFound", err)
	}
	token := &botflota.AccessToken{
		Name: "dashboard", Hash: []byte{7, 8},
		Scopes: []string{"ventas", "soporte"}, CreatedAt: t0, ExpiresAt: t2,
	}
	if err := db.AccessTokens().Create(ctx, token); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if token.ID == 0 {
		t.Fatal("Create did not set an id")
	}
	got, err := db.AccessTokens().FindByHash(ctx, token.Hash)
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if got.ID != token.ID || got.Name != "dashboard" || !got.ExpiresAt.Equal(t2) {
		t.Fatalf("token = %#v", got)
	}
	if !reflect.DeepEqual(got.Scopes, []string{"soporte", "ventas"}) {
		t.Fatalf("scopes = %#v, want stable order", got.Scopes)
	}
	if !got.Active(t1) {
		t.Fatal("token should be active before expiry")
	}
	if got.Active(t2) {
		t.Fatal("token should be expired at expires_at")
	}
	tokens, err := db.AccessTokens().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tokens) != 1 || len(tokens[0].Scopes) != 2 {
		t.Fatalf("List = %#v", tokens)
	}
	if len(tokens[0].Hash) != 0 {
		t.Fatal("List must not expose hashes")
	}
	if err := db.AccessTokens().Revoke(ctx, token.ID, t1); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, err = db.AccessTokens().FindByHash(ctx, token.Hash)
	if err != nil {
		t.Fatalf("FindByHash after revoke: %v", err)
	}
	if got.Active(t1) {
		t.Fatal("revoked token still active")
	}
	if err := db.AccessTokens().Revoke(ctx, token.ID, t2); err != nil {
		t.Fatalf("Revoke again: %v", err)
	}
	if err := db.AccessTokens().Revoke(ctx, 999, t1); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Revoke unknown = %v, want ErrNotFound", err)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))

	if _, err := db.Chats().Ensure(ctx, "support", "support-bot", 999, t0); err == nil {
		t.Fatal("Ensure with unknown sender succeeded, want a foreign key error")
	}

	orphanChat := &botflota.Message{
		ChatID: 999, Direction: botflota.Inbound, Kind: botflota.KindText,
		Body: "orphan", At: t1,
	}
	if err := db.Messages().Append(ctx, orphanChat); err == nil {
		t.Fatal("Append with unknown chat succeeded, want a foreign key error")
	}

	chat := ensureChat(t, db)
	orphanSession := &botflota.Message{
		ChatID: chat.ID, SessionID: 999, Direction: botflota.Inbound,
		Kind: botflota.KindText, Body: "orphan", At: t1,
	}
	if err := db.Messages().Append(ctx, orphanSession); err == nil {
		t.Fatal("Append with unknown session succeeded, want a foreign key error")
	}
}
