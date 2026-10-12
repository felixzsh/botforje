package fleet

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

// TestReopenedFleetSendsToThePersistedConversation is the P1 runtime gate: a
// reply an API send queued to a destination whose participant is unknown
// survives the database being closed and reopened, and a fresh fleet with no
// in-memory state from the earlier run delivers it to that persisted target and
// records the outcome (ADR 0051).
func TestReopenedFleetSendsToThePersistedConversation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "app.db")
	target := botflota.ChatTarget{
		Provider: "signal", Kind: botflota.ChatDirect, Thread: "thread-77",
	}

	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// The destination never wrote first, so the send alone registers its chat:
	// that persisted target is the knowledge a later run must trust (ADR 0051).
	queued, err := first.Sends().CommitSend(ctx, botflota.SendRecord{
		Scope: "dev", Bot: "alpha", Target: target,
		Content: botflota.TextContent{Body: "queued before restart"}, At: t0,
	})
	if err != nil {
		t.Fatalf("CommitSend: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	channels := map[string]*fakeChannel{}
	runner, err := New(loadFleetConfig(t), Options{
		Store: reopened.Sessions(), Chats: reopened.Chats(), Peers: reopened.Peers(),
		Messages: reopened.Messages(), Turn: reopened.Turns(), Outbox: reopened.Outbox(),
		DefaultTimeout: time.Minute, Now: func() time.Time { return t0 },
		NewChannel: func(spec BotSpec) (botflota.MessageChannel, error) {
			channel := newFakeChannel()
			channels[spec.Name] = channel
			return channel, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runDone := make(chan error, 1)
	var stopOnce sync.Once
	var stopErr error
	stop := func() error {
		stopOnce.Do(func() {
			cancel()
			select {
			case stopErr = <-runDone:
			case <-time.After(2 * time.Second):
				stopErr = errors.New("fleet did not stop within 2 seconds")
			}
		})
		return stopErr
	}
	// Cleanup runs in reverse order: join the fleet before closing SQLite,
	// including when a connection or delivery assertion fails.
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("stop fleet: %v", err)
		}
	})
	go func() { runDone <- runner.Run(ctx) }()
	waitConnected(t, runner)

	sent := receive(t, channels["alpha"].sent)
	if sent.Target != target {
		t.Fatalf("delivered to %#v, want the persisted conversation %#v",
			sent.Target, target)
	}
	if body := botflota.ContentText(sent.Content); body != "queued before restart" {
		t.Fatalf("delivered body = %q, want the queued reply", body)
	}
	// Stop before reading the outcome: the worker records the send after the
	// channel returns, so only a finished run proves the queue moved on.
	if err := stop(); err != nil {
		t.Fatalf("stop fleet: %v", err)
	}
	read := context.Background()
	var state string
	if err := reopened.QueryRowContext(read,
		"SELECT state FROM outbox WHERE id = ?", queued.OutboxID).Scan(&state); err != nil {
		t.Fatalf("read outbox state: %v", err)
	}
	if state != "sent" {
		t.Fatalf("outbox state = %q, want sent", state)
	}
	chat, err := reopened.Chats().ForTarget(read, "dev", "alpha", target)
	if err != nil {
		t.Fatalf("ForTarget: %v", err)
	}
	stored, err := reopened.Messages().History(read, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	var recorded *botflota.Message
	for i := range stored {
		if stored[i].ID == queued.MessageID {
			recorded = &stored[i]
		}
	}
	if recorded == nil {
		t.Fatalf("message %d is missing from %+v", queued.MessageID, stored)
	}
	if recorded.Target != target ||
		recorded.ProviderMessageID != "wamid.queued before restart" {
		t.Fatalf("stored message = %+v, want the target and the channel id", recorded)
	}
}

// TestOneSenderInTwoConversationsHasIndependentState is the P1 gate: the same
// sender in two supported conversations keeps independent chats, sessions, and
// holds, and a thread that differs from the sender is addressed exactly. The
// subjects here are opaque and nonnumeric, so nothing depends on a phone or a
// device locator (ADR 0051).
func TestOneSenderInTwoConversationsHasIndependentState(t *testing.T) {
	runner, channels, store := buildFleetWithStore(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()
	waitConnected(t, runner)
	alpha := channels["alpha"]

	sender := botflota.IdentityKey{Provider: "signal", Subject: "opaque:svc/9"}
	first := botflota.ChatTarget{
		Provider: "signal", Kind: botflota.ChatDirect, Thread: "thread-77",
	}
	second := botflota.ChatTarget{
		Provider: "signal", Kind: botflota.ChatDirect, Thread: "thread-78",
	}
	for _, target := range []botflota.ChatTarget{first, second} {
		alpha.incoming <- botflota.IncomingMessage{
			Bot: "alpha", Sender: sender, Target: target,
			Content: botflota.TextContent{Body: "hi"},
		}
		sent := receive(t, alpha.sent)
		if sent.Target != target {
			t.Fatalf("reply target = %#v, want the inbound conversation %#v",
				sent.Target, target)
		}
	}

	store.mu.Lock()
	chats, sessions := len(store.chats), len(store.sessions)
	store.mu.Unlock()
	if chats != 2 || sessions != 2 {
		t.Fatalf("chats/sessions = %d/%d, want one of each per conversation",
			chats, sessions)
	}

	// Holding one conversation leaves the other's control alone.
	if err := runner.SetChatHuman(ctx, "dev", "alpha", first, true); err != nil {
		t.Fatalf("SetChatHuman: %v", err)
	}
	if state, ok := runner.ChatControl("dev", "alpha", first); !ok ||
		state.Mode != botflota.ChatHuman {
		t.Fatalf("held control = %+v, %v; want human", state, ok)
	}
	if state, ok := runner.ChatControl("dev", "alpha", second); !ok ||
		state.Mode != botflota.ChatAuto {
		t.Fatalf("other control = %+v, %v; want auto", state, ok)
	}

	// A message into the held conversation is stored without a reply.
	alpha.incoming <- botflota.IncomingMessage{
		Bot: "alpha", Sender: sender, Target: first,
		Content: botflota.TextContent{Body: "held?"},
	}
	select {
	case sent := <-alpha.sent:
		t.Fatalf("held conversation answered with %#v", sent)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
}
