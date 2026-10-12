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

// TestFailPendingForTargetLeavesOtherDestinations proves a hand-over cancels the
// replies addressed to the conversation it held — including one whose history
// another chat records — and leaves every other destination's replies waiting,
// with the worker's recheck seeing the cancellation (ADR 0043, ADR 0051).
func TestFailPendingForTargetLeavesOtherDestinations(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	source := ensureChat(t, db)
	// The destination is addressed by a reply recorded under the source chat,
	// and by a chat of its own.
	destinationPeer, _, err := db.Peers().RecordInbound(ctx, "support",
		waKey("5215500000001"), "Beto", t0)
	if err != nil {
		t.Fatalf("RecordInbound: %v", err)
	}
	destinationTarget := waTarget("5215500000001")
	if _, err := db.Chats().Ensure(ctx, "support", "support-bot",
		destinationTarget, destinationPeer.ID, t0); err != nil {
		t.Fatalf("Ensure destination chat: %v", err)
	}
	queueReply(t, db, source, source.Target, "source reply")
	queueReply(t, db, source, destinationTarget, "alternate reply")
	queueReply(t, db, source, destinationTarget, "destination reply")

	before, err := db.Outbox().NextPending(ctx, "support", "support-bot", 10)
	if err != nil {
		t.Fatalf("NextPending: %v", err)
	}
	if len(before) != 3 {
		t.Fatalf("pending = %d, want 3", len(before))
	}
	count, err := db.Outbox().FailPendingForTarget(ctx, "support", "support-bot",
		destinationTarget, "hand-over", t2)
	if err != nil {
		t.Fatalf("FailPendingForTarget: %v", err)
	}
	if count != 2 {
		t.Fatalf("failed = %d, want the destination's two replies", count)
	}
	for _, queued := range before {
		pending, err := db.Outbox().IsPending(ctx, queued.ID)
		if err != nil {
			t.Fatalf("IsPending: %v", err)
		}
		wantPending := queued.Target == source.Target
		if pending != wantPending {
			t.Fatalf("reply to %s: pending = %v, want %v",
				queued.Target.Thread, pending, wantPending)
		}
	}
}

func queueReply(
	t *testing.T, db *sqlite.DB, chat *botflota.Chat, target botflota.ChatTarget,
	body string,
) {
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
			Target: target,
			Message: &botflota.Message{
				ChatID: chat.ID, Target: target, Direction: botflota.Outbound,
				Content: botflota.TextContent{Body: body}, At: t1,
				Ingest: botflota.IngestLive,
			},
		}},
	})
	if err != nil {
		t.Fatalf("queue reply: %v", err)
	}
}
