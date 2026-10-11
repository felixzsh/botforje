package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

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
