package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

// TestOpaqueIdentityAndTargetSurviveReopen is the P1 gate: an opaque
// nonnumeric subject and a thread that differs from it need no phone or device
// field, a fresh open reads the persisted conversation back with no sender
// cache, and two store-local spaces that both use the same local id stay apart
// (ADR 0051).
func TestOpaqueIdentityAndTargetSurviveReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	sender := botflota.IdentityKey{Provider: "signal", Subject: "opaque:svc/9"}
	target := botflota.ChatTarget{
		Provider: "signal", Kind: botflota.ChatDirect, Thread: "thread-77",
	}
	peer, _, err := db.Peers().RecordInbound(ctx, "dev", sender, "Ana", t0)
	if err != nil {
		t.Fatalf("RecordInbound: %v", err)
	}
	chat, err := db.Chats().Ensure(ctx, "dev", "alpha", target, peer.ID, t0)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	err = db.Turns().CommitTurn(ctx, botflota.TurnRecord{
		Scope: "dev", Bot: "alpha",
		Session: testSession(chat.ID), ChatID: chat.ID, At: t1,
		Inbound: &botflota.Message{
			ChatID: chat.ID, Target: target, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "hi"}, At: t1,
			Ingest: botflota.IngestLive,
		},
		Outbound: []botflota.OutboundRecord{{
			Target: target,
			Message: &botflota.Message{
				ChatID: chat.ID, Target: target, Direction: botflota.Outbound,
				Content: botflota.TextContent{Body: "hello"}, At: t1,
				Ingest: botflota.IngestLive,
			},
		}},
	})
	if err != nil {
		t.Fatalf("CommitTurn: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A fresh open holds no sender cache: the persisted target is the only
	// address the worker needs to deliver what the conversation still owes.
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.Chats().ForTarget(ctx, "dev", "alpha", target)
	if err != nil {
		t.Fatalf("ForTarget after reopen: %v", err)
	}
	if got.ID != chat.ID || got.PeerID != peer.ID {
		t.Fatalf("chat = %#v, want id %d and peer %d", got, chat.ID, peer.ID)
	}
	if _, err := reopened.Peers().Get(ctx, "dev", sender); err != nil {
		t.Fatalf("peer after reopen: %v", err)
	}
	entries, err := reopened.Outbox().NextPending(ctx, "dev", "alpha", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v; want the queued reply", entries, err)
	}
	if entries[0].Target != target {
		t.Fatalf("queued target = %#v, want %#v", entries[0].Target, target)
	}

	// Two store-local spaces that both use local id 7 are independent peers and
	// conversations, because the adapter composes the space into the subject.
	spaceA := botflota.IdentityKey{Provider: "simplex", Subject: "space-a/7"}
	spaceB := botflota.IdentityKey{Provider: "simplex", Subject: "space-b/7"}
	first, err := reopened.Peers().EnsurePeer(ctx, "dev", spaceA, t0)
	if err != nil {
		t.Fatalf("EnsurePeer space-a: %v", err)
	}
	second, err := reopened.Peers().EnsurePeer(ctx, "dev", spaceB, t0)
	if err != nil {
		t.Fatalf("EnsurePeer space-b: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("two local spaces sharing local id 7 collapsed into one peer")
	}
	threadA := botflota.ChatTarget{Provider: "simplex", Kind: botflota.ChatDirect, Thread: "space-a/7"}
	threadB := botflota.ChatTarget{Provider: "simplex", Kind: botflota.ChatDirect, Thread: "space-b/7"}
	chatA, err := reopened.Chats().Ensure(ctx, "dev", "alpha", threadA, first.ID, t0)
	if err != nil {
		t.Fatalf("Ensure space-a chat: %v", err)
	}
	chatB, err := reopened.Chats().Ensure(ctx, "dev", "alpha", threadB, second.ID, t0)
	if err != nil {
		t.Fatalf("Ensure space-b chat: %v", err)
	}
	if chatA.ID == chatB.ID {
		t.Fatal("two local spaces sharing one thread id collapsed into one chat")
	}
}

// TestAlternateDestinationSharesNoIDWithTheSourceChat is the other half of the
// P1 gate: a channel id the source chat already holds belongs to a different
// destination and stores there, while the same id arriving in that destination
// again is the duplicate (ADR 0051).
func TestAlternateDestinationSharesNoIDWithTheSourceChat(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	source := ensureChat(t, db)
	destination := waTarget("5215500000001")
	if err := db.Messages().Append(ctx, &botflota.Message{
		ChatID: source.ID, Target: source.Target, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "in"}, At: t0,
		ProviderMessageID: "overlap", Ingest: botflota.IngestLive,
	}); err != nil {
		t.Fatalf("Append source: %v", err)
	}
	destinationChat, err := db.Chats().Ensure(ctx, "support", "support-bot",
		destination, 0, t0)
	if err != nil {
		t.Fatalf("Ensure destination: %v", err)
	}
	// The reply goes to the alternate destination with the id the source chat
	// already holds; the two are different messages.
	if err := db.Messages().Append(ctx, &botflota.Message{
		ChatID: destinationChat.ID, Target: destination,
		Direction: botflota.Outbound, Content: botflota.TextContent{Body: "out"},
		At: t1, ProviderMessageID: "overlap", Ingest: botflota.IngestLive,
	}); err != nil {
		t.Fatalf("Append alternate destination: %v", err)
	}
	// The destination's own copy of the id is the duplicate.
	if err := db.Messages().Append(ctx, &botflota.Message{
		ChatID: destinationChat.ID, Target: destination,
		Direction: botflota.Inbound, Content: botflota.TextContent{Body: "echo"},
		At: t1, ProviderMessageID: "overlap", Ingest: botflota.IngestSync,
	}); !errors.Is(err, botflota.ErrDuplicate) {
		t.Fatalf("destination echo = %v, want ErrDuplicate", err)
	}
}
