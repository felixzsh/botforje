package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

func spacedMessage(
	chat *botflota.Chat, space, id string,
) *botflota.Message {
	return &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "hola"}, At: t1,
		ProviderMessageID: id, MessageSpace: space, Ingest: botflota.IngestLive,
	}
}

// TestMessageSpaceScopesProviderIDs covers the durable dedup key: the scope,
// the bot, the adapter-declared message space, the complete destination, and
// the provider id (ADR 0026, ADR 0051). A reused id in another destination
// or another space is a distinct row; only the full key repeats.
func TestMessageSpaceScopesProviderIDs(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	other := waTarget("5215500000001")
	otherChat, err := db.Chats().Ensure(ctx, "support", "support-bot", other, 0, t0)
	if err != nil {
		t.Fatalf("Ensure other chat: %v", err)
	}
	appendTo := func(chat *botflota.Chat, space, id string) error {
		message := spacedMessage(chat, space, id)
		message.Target = chat.Target
		return db.Messages().Append(ctx, message)
	}
	if err := appendTo(chat, "space-a", "wamid.1"); err != nil {
		t.Fatalf("first append: %v", err)
	}
	for _, tc := range []struct {
		name  string
		chat  *botflota.Chat
		space string
		want  bool
	}{
		{"same space and destination repeats", chat, "space-a", true},
		{"another space is distinct", chat, "space-b", false},
		{"another destination is distinct", otherChat, "space-a", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := appendTo(tc.chat, tc.space, "wamid.1")
			if errors.Is(err, botflota.ErrDuplicate) != tc.want {
				t.Fatalf("append = %v, want duplicate %v", err, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		space string
		want  bool
	}{
		{"recorded space", "space-a", true},
		{"another space", "space-b", true},
		{"unknown space", "space-c", false},
	} {
		t.Run("has "+tc.name, func(t *testing.T) {
			got, err := db.Messages().HasProviderID(ctx, "support",
				"support-bot", tc.space, chat.Target, "wamid.1")
			if err != nil || got != tc.want {
				t.Fatalf("HasProviderID = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// TestMessageSpaceSurvivesReopen proves the space and the dedup behavior
// live in the database, not in process memory: after a close and reopen the
// recorded spaces still scope their provider ids.
func TestMessageSpaceSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	chat := ensureChatOn(t, db, "support", "support-bot")
	if err := db.Messages().Append(ctx, spacedMessage(chat, "space-a", "wamid.9")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	for _, tc := range []struct {
		name  string
		space string
		want  bool
	}{
		{"recorded space still matches", "space-a", true},
		{"another space still distinct", "space-b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reopened.Messages().HasProviderID(ctx, "support",
				"support-bot", tc.space, chat.Target, "wamid.9")
			if err != nil || got != tc.want {
				t.Fatalf("HasProviderID = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	stored, err := reopened.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil || len(stored) != 1 || stored[0].MessageSpace != "space-a" {
		t.Fatalf("History = %+v, %v; want the spaced row", stored, err)
	}
	again := spacedMessage(chat, "space-a", "wamid.9")
	if err := reopened.Messages().Append(ctx, again); !errors.Is(err, botflota.ErrDuplicate) {
		t.Fatalf("reopened duplicate = %v, want ErrDuplicate", err)
	}
}

func ensureChatOn(
	t *testing.T, db *sqlite.DB, scope, bot string,
) *botflota.Chat {
	t.Helper()
	contact, _, err := db.Peers().RecordInbound(context.Background(), scope,
		waKey("5215500000000"), "Ana", t0)
	if err != nil {
		t.Fatalf("RecordInbound: %v", err)
	}
	chat, err := db.Chats().Ensure(context.Background(), scope, bot,
		waChatTarget, contact.ID, t0)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return chat
}

// TestMoveBotKeepsMessageProvenance proves a migration re-keys ownership
// without rewriting provenance: the moved message keeps the space and the
// destination of the account that recorded it (ADR 0026, ADR 0051).
func TestMoveBotKeepsMessageProvenance(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := seedHistory(t, db, "lost-bot")
	message := &botflota.Message{
		ChatID: chat.ID, Target: waTarget("5215500000001"),
		Direction: botflota.Inbound,
		Content:   botflota.TextContent{Body: "moved"}, At: t1,
		ProviderMessageID: "wamid.M", MessageSpace: "first-account",
		Ingest: botflota.IngestSync,
	}
	if err := db.Messages().Append(ctx, message); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := db.History().MoveBot(ctx, "support", "lost-bot", "new-bot", t2); err != nil {
		t.Fatalf("MoveBot: %v", err)
	}
	stored, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	var moved *botflota.Message
	for i := range stored {
		if stored[i].ProviderMessageID == "wamid.M" {
			moved = &stored[i]
		}
	}
	if moved == nil {
		t.Fatalf("moved message is missing: %+v", stored)
	}
	if moved.MessageSpace != "first-account" ||
		moved.Target != waTarget("5215500000001") {
		t.Fatalf("migration rewrote provenance: %+v", moved)
	}
	for _, tc := range []struct {
		name  string
		bot   string
		space string
		want  bool
	}{
		{"new owner keeps the old space", "new-bot", "first-account", true},
		{"new owner has no new space", "new-bot", "second-account", false},
		{"old owner lost the row", "lost-bot", "first-account", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.Messages().HasProviderID(ctx, "support", tc.bot,
				tc.space, waTarget("5215500000001"), "wamid.M")
			if err != nil || got != tc.want {
				t.Fatalf("HasProviderID = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// TestOutboxMarkSentKeepsMessageSpace proves a reply's provider id is
// recorded under the space of the binding that admitted it: the row queued
// by the turn keeps its space when the delivery marks it sent.
func TestOutboxMarkSentKeepsMessageSpace(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	session := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("Save session: %v", err)
	}
	turn := botflota.TurnRecord{
		Scope: "support", Bot: "support-bot", Session: session,
		ChatID: chat.ID, At: t1, BindingGeneration: 2,
		Inbound: &botflota.Message{
			ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "hi"}, At: t1,
			ProviderMessageID: "wamid.in", MessageSpace: "account-a",
			Ingest: botflota.IngestLive,
		},
		Outbound: []botflota.OutboundRecord{{
			Target: chat.Target,
			Message: &botflota.Message{
				ChatID: chat.ID, Target: chat.Target,
				Direction: botflota.Outbound,
				Content:   botflota.TextContent{Body: "hello"}, At: t1,
				MessageSpace: "account-a", Ingest: botflota.IngestLive,
				Origin: botflota.OriginOperator, Routing: botflota.RoutingRoot,
			},
		}},
	}
	if err := db.Turns().CommitTurn(ctx, turn); err != nil {
		t.Fatalf("CommitTurn: %v", err)
	}
	pending, err := db.Outbox().NextPending(ctx, "support", "support-bot", 1)
	if err != nil || len(pending) != 1 {
		t.Fatalf("NextPending = %+v, %v; want one reply", pending, err)
	}
	if err := db.Outbox().MarkSent(ctx, pending[0].ID, "wamid.out", t2); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	stored, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	var sent *botflota.Message
	for i := range stored {
		if stored[i].Direction == botflota.Outbound {
			sent = &stored[i]
		}
	}
	if sent == nil {
		t.Fatalf("sent reply is missing: %+v", stored)
	}
	if sent.ProviderMessageID != "wamid.out" || sent.MessageSpace != "account-a" {
		t.Fatalf("sent provenance = %+v, want wamid.out under account-a", sent)
	}
	if got, err := db.Messages().HasProviderID(ctx, "support", "support-bot",
		"account-a", chat.Target, "wamid.out"); err != nil || !got {
		t.Fatalf("HasProviderID = %v, %v; want true", got, err)
	}
}
