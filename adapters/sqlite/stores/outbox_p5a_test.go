package stores_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

// commitBinding stores one binding and reports the generation the store
// assigned, the value admission snapshots onto the queue.
func commitBinding(t *testing.T, db *sqlite.DB) int64 {
	t.Helper()
	account := &botflota.Binding{
		Scope: "support", Bot: "support-bot",
		Provider: "whatsapp", Adapter: "whatsmeow", Mode: "native",
		Identity: waKey("5215500000000"),
	}
	if err := db.Bindings().Commit(context.Background(), account); err != nil {
		t.Fatalf("Commit binding: %v", err)
	}
	stored, err := db.Bindings().Get(context.Background(), "support", "support-bot")
	if err != nil {
		t.Fatalf("Get binding: %v", err)
	}
	return stored.Generation
}

func queueTurnWithGeneration(
	t *testing.T, db *sqlite.DB, chat *botflota.Chat, generation int64,
) {
	t.Helper()
	err := db.Turns().CommitTurn(context.Background(), botflota.TurnRecord{
		Scope: "support", Bot: "support-bot",
		BindingGeneration: generation,
		Session:           testSession(chat.ID), ChatID: chat.ID, At: t1,
		Inbound: &botflota.Message{
			ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "hola"}, At: t1,
			Ingest: botflota.IngestLive,
		},
		Outbound: []botflota.OutboundRecord{{
			Target: chat.Target,
			Message: &botflota.Message{
				ChatID: chat.ID, Target: chat.Target, Direction: botflota.Outbound,
				Content: botflota.TextContent{Body: "hola!"}, At: t1,
				Ingest: botflota.IngestLive,
			},
		}},
	})
	if err != nil {
		t.Fatalf("CommitTurn: %v", err)
	}
}

// TestOutboxSnapshotsGenerationAndDeliveryKey: every queued delivery carries
// the admitted binding generation and a minted idempotency key, and two
// deliveries never share a key (ADR 0040, ADR 0052).
func TestOutboxSnapshotsGenerationAndDeliveryKey(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	generation := commitBinding(t, db)
	queueTurnWithGeneration(t, db, chat, generation)

	result, err := db.Sends().CommitSend(ctx, botflota.SendRecord{
		Scope: "support", Bot: "support-bot", Target: chat.Target,
		Content:           botflota.TextContent{Body: "notice"},
		BindingGeneration: generation, At: t1,
	})
	if err != nil {
		t.Fatalf("CommitSend: %v", err)
	}
	if result.OutboxID == 0 {
		t.Fatal("CommitSend reported no queued delivery")
	}
	entries, err := db.Outbox().NextPending(ctx, "support", "support-bot", 10)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %v, %v, want both queued deliveries", entries, err)
	}
	for _, entry := range entries {
		if entry.BindingGeneration != generation {
			t.Fatalf("entry %d generation = %d, want %d",
				entry.ID, entry.BindingGeneration, generation)
		}
		if entry.DeliveryKey == "" {
			t.Fatalf("entry %d has no delivery key", entry.ID)
		}
	}
	if entries[0].DeliveryKey == entries[1].DeliveryKey {
		t.Fatalf("delivery keys collide: %q", entries[0].DeliveryKey)
	}
}

// TestOutboxDeliveryKeySurvivesRetry: scheduling the next attempt never
// mints a new key; the attempts of one delivery share it (ADR 0052).
func TestOutboxDeliveryKeySurvivesRetry(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	queueTurnWithGeneration(t, db, chat, commitBinding(t, db))

	entries, err := db.Outbox().NextPending(ctx, "support", "support-bot", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	key := entries[0].DeliveryKey
	if err := db.Outbox().MarkRetry(ctx, entries[0].ID, "rejected", t2, t1); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	scheduled, err := db.Outbox().NextPending(ctx, "support", "support-bot", 1)
	if err != nil || len(scheduled) != 1 {
		t.Fatalf("a scheduled retry left the queue: %v, %v", scheduled, err)
	}
	if scheduled[0].DeliveryKey != key {
		t.Fatalf("delivery key = %q, want the queued %q", scheduled[0].DeliveryKey, key)
	}
}

// TestOutboxReplacementFailsOldQueue: replacing the binding fails its
// previous entries, so the new account sends none of the old queue.
func TestOutboxReplacementFailsOldQueue(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	queueTurnWithGeneration(t, db, chat, commitBinding(t, db))

	// The replacement settles what the old link owes before the new one
	// commits, the way retireStaleLink does (ADR 0040).
	ended, err := db.Outbox().FailPending(
		ctx, "support", "support-bot", "account unlinked", t2)
	if err != nil || ended != 1 {
		t.Fatalf("FailPending = %d, %v, want the one queued reply", ended, err)
	}
	if depth, err := db.Outbox().Depth(ctx, "support", "support-bot"); err != nil ||
		depth != 0 {
		t.Fatalf("depth = %d, %v, want nothing for the new account", depth, err)
	}
	pending, err := db.Outbox().NextPending(ctx, "support", "support-bot", 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %v, %v, want none", pending, err)
	}
}

// TestOutboxEntrySurvivesRestartWithSnapshot: a queued entry outlives a
// restart with its generation snapshot and delivery key intact, so the next
// run still honors them (ADR 0040, ADR 0052).
func TestOutboxEntrySurvivesRestartWithSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db := open(t, path)
	chat := ensureChat(t, db)
	generation := commitBinding(t, db)
	queueTurnWithGeneration(t, db, chat, generation)
	before, err := db.Outbox().NextPending(ctx, "support", "support-bot", 1)
	if err != nil || len(before) != 1 {
		t.Fatalf("NextPending = %v, %v", before, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	after, err := reopened.Outbox().NextPending(ctx, "support", "support-bot", 1)
	if err != nil || len(after) != 1 {
		t.Fatalf("NextPending after restart = %v, %v", after, err)
	}
	if after[0].BindingGeneration != generation {
		t.Fatalf("generation = %d, want the admitted %d",
			after[0].BindingGeneration, generation)
	}
	if after[0].DeliveryKey == "" || after[0].DeliveryKey != before[0].DeliveryKey {
		t.Fatalf("delivery key = %q, want the queued %q",
			after[0].DeliveryKey, before[0].DeliveryKey)
	}
	stored, err := reopened.Bindings().Get(ctx, "support", "support-bot")
	if err != nil || stored.Generation != generation {
		t.Fatalf("binding = %#v, %v, want generation %d", stored, err, generation)
	}
}

// TestOutboxUncertainTerminalWording: the terminal record of an uncertain
// delivery says acceptance was not confirmed; the wording is what the
// worker writes on its last uncertain attempt (ADR 0052).
func TestOutboxUncertainTerminalWording(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	queueTurnWithGeneration(t, db, chat, commitBinding(t, db))

	entries, err := db.Outbox().NextPending(ctx, "support", "support-bot", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	reason := "acceptance not confirmed: timeout after send"
	if err := db.Outbox().MarkFailed(ctx, entries[0].ID, reason, t2); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	var stored string
	err = db.QueryRowContext(ctx,
		`SELECT error FROM outbox WHERE id = ?`, entries[0].ID).Scan(&stored)
	if err != nil {
		t.Fatalf("read failed row: %v", err)
	}
	if !strings.Contains(stored, "acceptance not confirmed") {
		t.Fatalf("terminal record = %q, want acceptance-not-confirmed", stored)
	}
}
