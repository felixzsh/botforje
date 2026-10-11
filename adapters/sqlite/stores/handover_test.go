package stores_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

// TestRecordStoresWithoutASession covers the non-turn write a hand-over needs:
// history lands, no session run is created, and only an inbound counts as a
// live message (ADR 0043).
func TestRecordStoresWithoutASession(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	inbound := &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "held"}, At: t1,
		ProviderMessageID: "held-1", Ingest: botflota.IngestLive,
	}
	if err := db.Turns().Record(ctx, inbound, t1); err != nil {
		t.Fatalf("Record inbound: %v", err)
	}
	if inbound.ID == 0 || inbound.SessionID != 0 {
		t.Fatalf("inbound = %#v, want stored with no session", inbound)
	}
	if _, err := db.Sessions().Latest(ctx, chat.ID); err == nil {
		t.Fatal("Record created a session run")
	}
	outbound := &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, Direction: botflota.Outbound,
		Content: botflota.TextContent{Body: "te atiendo"}, At: t2,
		ProviderMessageID: "op-1", Ingest: botflota.IngestLive,
		Origin: botflota.OriginOperator,
	}
	if err := db.Turns().Record(ctx, outbound, t2); err != nil {
		t.Fatalf("Record outbound: %v", err)
	}
	chats, err := db.Chats().Recent(ctx, "support", "support-bot", 0)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if chats[0].MessageCount != 1 {
		t.Fatalf("message count = %d, want only the inbound", chats[0].MessageCount)
	}
	if !chats[0].UpdatedAt.Equal(t2) {
		t.Fatalf("updated_at = %s, want %s", chats[0].UpdatedAt, t2)
	}
}

// TestFailPendingForChatLeavesOtherChats proves a hand-over cancels only its
// own chat's queued replies, and that the worker's recheck sees the
// cancellation (ADR 0043).
func TestFailPendingForChatLeavesOtherChats(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	other := ensureChatFor(t, db, "5215500000001", "Beto")
	queueReply(t, db, chat, "chat reply")
	queueReply(t, db, other, "other reply")

	before, err := db.Outbox().NextPending(ctx, "support", "support-bot", 10)
	if err != nil {
		t.Fatalf("NextPending: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("pending = %d, want 2", len(before))
	}
	count, err := db.Outbox().FailPendingForChat(ctx, chat.ID, "hand-over", t2)
	if err != nil {
		t.Fatalf("FailPendingForChat: %v", err)
	}
	if count != 1 {
		t.Fatalf("failed = %d, want the chat's one reply", count)
	}
	gone, err := db.Outbox().IsPending(ctx, before[0].ID)
	if err != nil {
		t.Fatalf("IsPending: %v", err)
	}
	if gone {
		t.Fatal("the cancelled reply still reports pending")
	}
	kept, err := db.Outbox().IsPending(ctx, before[1].ID)
	if err != nil {
		t.Fatalf("IsPending: %v", err)
	}
	if !kept {
		t.Fatal("the other chat's reply was cancelled too")
	}
}

func ensureChatFor(t *testing.T, db *sqlite.DB, phone, name string) *botflota.Chat {
	t.Helper()
	contact, _, err := db.Peers().RecordInbound(
		context.Background(), "support", waKey(phone), name, t0)
	if err != nil {
		t.Fatalf("RecordMessage contact: %v", err)
	}
	chat, err := db.Chats().Ensure(context.Background(), "support", "support-bot",
		waTarget(phone), contact.ID, t0)
	if err != nil {
		t.Fatalf("Ensure chat: %v", err)
	}
	return chat
}

func queueReply(t *testing.T, db *sqlite.DB, chat *botflota.Chat, body string) {
	t.Helper()
	ctx := context.Background()
	err := db.Turns().CommitTurn(ctx, botflota.TurnRecord{
		Scope: "support", Bot: "support-bot",
		Session: testSession(chat.ID), ChatID: chat.ID, At: t1,
		Inbound: &botflota.Message{
			ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "in"}, At: t1,
			Ingest: botflota.IngestLive,
		},
		Outbound: []botflota.OutboundRecord{{
			Target: chat.Target,
			Message: &botflota.Message{
				ChatID: chat.ID, Target: chat.Target, Direction: botflota.Outbound,
				Content: botflota.TextContent{Body: body}, At: t1,
				Ingest: botflota.IngestLive,
			},
		}},
	})
	if err != nil {
		t.Fatalf("queue reply: %v", err)
	}
}
