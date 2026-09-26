package fleet

import (
	"context"
	"errors"
	"testing"

	"github.com/felixzsh/botflota"
)

// queuedReplyIDFor finds the queued reply stored under one sender's chat, so a
// test can check whether that reply is still waiting.
func queuedReplyIDFor(
	t *testing.T, store *memStore, scope, bot, phone string,
) int64 {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	sender, ok := store.senders[senderKey(scope, phone)]
	if !ok {
		t.Fatalf("no sender stored for %s", phone)
	}
	var chatID int64
	for _, chat := range store.chats {
		if chat.Scope == scope && chat.Bot == bot && chat.SenderID == sender.ID {
			chatID = chat.ID
		}
	}
	if chatID == 0 {
		t.Fatalf("no chat stored for %s", phone)
	}
	stored := map[int64]bool{}
	for _, message := range store.messages {
		if message.ChatID == chatID {
			stored[message.ID] = true
		}
	}
	for _, queued := range store.outbox {
		if stored[queued.entry.MessageID] {
			return queued.entry.ID
		}
	}
	t.Fatalf("no reply queued for %s", phone)
	return 0
}

// TestSetChatHumanCancelsOnlyThatChatsQueue covers the hand-over's effect on the
// queue: holding one chat fails the replies that chat still owes and leaves
// every other chat's replies waiting (ADR 0043).
func TestSetChatHumanCancelsOnlyThatChatsQueue(t *testing.T) {
	// The channel blocks inside the first send, so both replies stay queued:
	// one is in flight and one waits behind it.
	runner, channels, store := buildFleetWithStore(t, "alpha", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()
	waitConnected(t, runner)
	alpha := channels["alpha"]

	for _, sender := range []string{"held", "other"} {
		alpha.incoming <- botflota.IncomingMessage{
			Bot: "alpha", SenderPhone: sender,
			Content: botflota.TextContent{Body: "hi"},
		}
	}
	<-alpha.entered

	heldID := queuedReplyIDFor(t, store, "dev", "alpha", "held")
	otherID := queuedReplyIDFor(t, store, "dev", "alpha", "other")

	if err := runner.SetChatHuman(ctx, "dev", "alpha", "held", true); err != nil {
		t.Fatalf("SetChatHuman: %v", err)
	}
	if pending, err := store.IsPending(ctx, heldID); err != nil || pending {
		t.Fatalf("the held chat's reply is still queued (pending=%v, err=%v)",
			pending, err)
	}
	if pending, err := store.IsPending(ctx, otherID); err != nil || !pending {
		t.Fatalf("another chat's reply was cancelled (pending=%v, err=%v)",
			pending, err)
	}

	state, ok := runner.ChatControl("dev", "alpha", "held")
	if !ok || state.Mode != botflota.ChatHuman {
		t.Fatalf("control = %+v, %v; want a human hold", state, ok)
	}
	if err := runner.SetChatHuman(ctx, "dev", "alpha", "held", false); err != nil {
		t.Fatalf("release: %v", err)
	}
	if state, _ := runner.ChatControl("dev", "alpha", "held"); state.Mode != botflota.ChatAuto {
		t.Fatalf("mode after release = %q, want auto", state.Mode)
	}

	close(alpha.release)
	cancel()
}

// TestSetChatHumanRefusesABotThatIsNotRunning: a stopped bot has no live state
// to hold, and says so instead of pretending (ADR 0043).
func TestSetChatHumanRefusesABotThatIsNotRunning(t *testing.T) {
	// Built but never run, so no bot holds an actor.
	runner, _, _ := buildFleetWithStore(t, "", nil)
	err := runner.SetChatHuman(context.Background(), "dev", "alpha", "s", true)
	if !errors.Is(err, botflota.ErrBotNotRunning) {
		t.Fatalf("error = %v, want ErrBotNotRunning", err)
	}
	if _, ok := runner.ChatControl("dev", "alpha", "s"); ok {
		t.Fatal("a stopped bot reported control state")
	}
}

// TestHandingOverFailsAReplyAlreadyInFlight covers the edge the recheck cannot
// reach: the worker had already left the queue with the reply when the chat was
// handed over, so the row is failed at once and the send still finishes, which
// is the best effort ADR 0043 promises.
func TestHandingOverFailsAReplyAlreadyInFlight(t *testing.T) {
	runner, channels, store := buildFleetWithStore(t, "alpha", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()
	waitConnected(t, runner)
	alpha := channels["alpha"]

	alpha.incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "held",
		Content: botflota.TextContent{Body: "hi"},
	}
	<-alpha.entered
	inFlight := queuedReplyIDFor(t, store, "dev", "alpha", "held")

	if err := runner.SetChatHuman(ctx, "dev", "alpha", "held", true); err != nil {
		t.Fatalf("SetChatHuman: %v", err)
	}
	if pending, err := store.IsPending(ctx, inFlight); err != nil || pending {
		t.Fatalf("an in-flight reply was left queued (pending=%v, err=%v)", pending, err)
	}

	// Letting the send finish does not undo the failure: what left was already
	// on its way, and the queue holds nothing more for that chat.
	close(alpha.release)
	if reply := receive(t, alpha.sent); botflota.ContentText(reply.Content) != "hello from alpha" {
		t.Fatalf("reply = %q, want the in-flight send to complete",
			botflota.ContentText(reply.Content))
	}
	cancel()
}
