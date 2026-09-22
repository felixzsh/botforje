package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

// queueOneReply stores one turn with a single reply, the shape every outbox
// test starts from.
func queueOneReply(t *testing.T, db *sqlite.DB, chat *botflota.Chat) {
	t.Helper()
	err := db.Turns().CommitTurn(context.Background(), botflota.TurnRecord{
		Scope: "support", Bot: "support-bot",
		Session: testSession(chat.ID), ChatID: chat.ID, At: t1,
		Inbound: &botflota.Message{
			ChatID: chat.ID, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "hola"}, At: t1,
			Ingest: botflota.IngestLive,
		},
		Outbound: []botflota.OutboundRecord{{
			Message: &botflota.Message{
				ChatID: chat.ID, Direction: botflota.Outbound,
				Content: botflota.TextContent{Body: "hola!"}, At: t1,
				Ingest: botflota.IngestLive,
			},
			Recipient: "5215500000000",
		}},
	})
	if err != nil {
		t.Fatalf("CommitTurn: %v", err)
	}
}

// TestOutboxQueuesWithTheTurnAndCompletes walks one reply from the turn that
// queued it to the recorded delivery, including the channel's own id landing
// on the stored message (ADR 0011, ADR 0026).
func TestOutboxQueuesWithTheTurnAndCompletes(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	queueOneReply(t, db, chat)

	depth, err := db.Outbox().Depth(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Depth: %v", err)
	}
	if depth != 1 {
		t.Fatalf("depth = %d, want the queued reply", depth)
	}
	entries, err := db.Outbox().NextPending(ctx, "support", "support-bot", 10)
	if err != nil {
		t.Fatalf("NextPending: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one", entries)
	}
	entry := entries[0]
	if entry.Recipient != "5215500000000" || entry.Attempts != 0 ||
		botflota.ContentText(entry.Content) != "hola!" {
		t.Fatalf("entry = %#v, want the stored reply", entry)
	}
	if !entry.NextAttempt.IsZero() {
		t.Fatalf("next attempt = %s, want a reply that is due now",
			entry.NextAttempt)
	}
	if other, err := db.Outbox().NextPending(
		ctx, "support", "other-bot", 10); err != nil || len(other) != 0 {
		t.Fatalf("another bot saw %v, %v, want nothing", other, err)
	}

	if err := db.Outbox().MarkSent(ctx, entry.ID, "wamid.1", t1); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	if depth, err := db.Outbox().Depth(ctx, "support", "support-bot"); err != nil ||
		depth != 0 {
		t.Fatalf("depth after send = %d, %v, want 0", depth, err)
	}
	messages, err := db.Messages().History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	var reply *botflota.Message
	for i := range messages {
		if messages[i].Direction == botflota.Outbound {
			reply = &messages[i]
		}
	}
	if reply == nil || reply.ProviderMessageID != "wamid.1" {
		t.Fatalf("reply = %#v, want the channel id recorded", reply)
	}
	if err := db.Outbox().MarkSent(ctx, entry.ID, "wamid.2", t1); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("second MarkSent = %v, want ErrNotFound", err)
	}
}

// TestOutboxSchedulesRetriesAndFailsForGood covers the retry states: a
// scheduled reply stays at the head of the queue with its next attempt
// recorded — it never leaves past the reply behind it — and once failed
// neither the queue nor a later update touches it again (ADR 0011).
func TestOutboxSchedulesRetriesAndFailsForGood(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	queueOneReply(t, db, chat)
	entries, err := db.Outbox().NextPending(ctx, "support", "support-bot", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	id := entries[0].ID

	if err := db.Outbox().MarkRetry(ctx, id, "rejected", t2, t1); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	scheduled, err := db.Outbox().NextPending(ctx, "support", "support-bot", 1)
	if err != nil || len(scheduled) != 1 {
		t.Fatalf("a scheduled reply left the queue: %v, %v", scheduled, err)
	}
	if !scheduled[0].NextAttempt.Equal(t2) {
		t.Fatalf("next attempt = %s, want the scheduled retry at %s",
			scheduled[0].NextAttempt, t2)
	}
	if scheduled[0].Attempts != 1 {
		t.Fatalf("attempts = %d, want the failed one counted",
			scheduled[0].Attempts)
	}

	if err := db.Outbox().MarkFailed(ctx, id, "gave up", t2); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if depth, err := db.Outbox().Depth(ctx, "support", "support-bot"); err != nil ||
		depth != 0 {
		t.Fatalf("depth after giving up = %d, %v, want 0", depth, err)
	}
	if due, err := db.Outbox().NextPending(
		ctx, "support", "support-bot", 1); err != nil || len(due) != 0 {
		t.Fatalf("a failed reply came back: %v, %v", due, err)
	}
	if err := db.Outbox().MarkRetry(
		ctx, id, "again", t2.Add(time.Hour), t2); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("MarkRetry after failed = %v, want ErrNotFound", err)
	}
}

// TestFailPendingEndsEveryQueuedReply: unlinking a bot ends its queue in
// one step and reports the count, so a later pairing has nothing to send
// from the old link (ADR 0040).
func TestFailPendingEndsEveryQueuedReply(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	queueOneReply(t, db, chat)

	ended, err := db.Outbox().FailPending(ctx, "support", "support-bot",
		"account unlinked", t2)
	if err != nil || ended != 1 {
		t.Fatalf("FailPending = %d, %v, want the one queued reply", ended, err)
	}
	if depth, err := db.Outbox().Depth(ctx, "support", "support-bot"); err != nil ||
		depth != 0 {
		t.Fatalf("depth = %d, %v, want nothing waiting", depth, err)
	}
	if pending, err := db.Outbox().NextPending(
		ctx, "support", "support-bot", 10); err != nil || len(pending) != 0 {
		t.Fatalf("pending = %v, %v, want none", pending, err)
	}
	if ended, err := db.Outbox().FailPending(ctx, "support", "support-bot",
		"account unlinked", t2); err != nil || ended != 0 {
		t.Fatalf("second FailPending = %d, %v, want nothing left", ended, err)
	}
}
