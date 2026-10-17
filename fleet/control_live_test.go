package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// queuedReplyIDFor finds the queued reply stored under one sender's chat, so a
// test can check whether that reply is still waiting. It waits for the bot to
// finish the turn: the run loop keeps reading while the channel blocks on the
// first send, so a second message may still be in flight when this is called.
func queuedReplyIDFor(
	t *testing.T, store *memStore, scope, bot, phone string,
) int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if id, ok := store.queuedReplyFor(scope, bot, phone); ok {
			return id
		}
		if time.Now().After(deadline) {
			t.Fatalf("no reply queued for %s", phone)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// queuedReplyFor reports the queued reply of one peer's chat, false while the
// turn that stores it has not landed yet.
func (s *memStore) queuedReplyFor(scope, bot, phone string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sender, ok := s.peers[peerKey(scope, waKey(phone))]
	if !ok {
		return 0, false
	}
	var chatID int64
	for _, chat := range s.chats {
		if chat.Scope == scope && chat.Bot == bot && chat.PeerID == sender.ID {
			chatID = chat.ID
		}
	}
	if chatID == 0 {
		return 0, false
	}
	stored := map[int64]bool{}
	for _, message := range s.messages {
		if message.ChatID == chatID {
			stored[message.ID] = true
		}
	}
	for _, queued := range s.outbox {
		if stored[queued.entry.MessageID] {
			return queued.entry.ID, true
		}
	}
	return 0, false
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
			Bot: "alpha", Sender: waKey(sender), Target: waTarget(sender),
			Content: botflota.TextContent{Body: "hi"},
		}
	}
	<-alpha.entered

	heldID := queuedReplyIDFor(t, store, "dev", "alpha", "held")
	otherID := queuedReplyIDFor(t, store, "dev", "alpha", "other")

	if err := runner.SetChatHuman(ctx, "dev", "alpha", waTarget("held"), true); err != nil {
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

	state, ok := runner.ChatControl("dev", "alpha", waTarget("held"))
	if !ok || state.Mode != botflota.ChatHuman {
		t.Fatalf("control = %+v, %v; want a human hold", state, ok)
	}
	if err := runner.SetChatHuman(ctx, "dev", "alpha", waTarget("held"), false); err != nil {
		t.Fatalf("release: %v", err)
	}
	state, ok = runner.ChatControl("dev", "alpha", waTarget("held"))
	if !ok || state.Mode != botflota.ChatAuto {
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
	err := runner.SetChatHuman(context.Background(), "dev", "alpha", waTarget("s"), true)
	if !errors.Is(err, botflota.ErrBotNotRunning) {
		t.Fatalf("error = %v, want ErrBotNotRunning", err)
	}
	if _, ok := runner.ChatControl("dev", "alpha", waTarget("s")); ok {
		t.Fatal("a stopped bot reported control state")
	}
}

// queueAPISend puts one queued API-origin send directly into the store, the
// way CommitSend does (ADR 0044), so the worker can be driven in tests.
func queueAPISend(
	t *testing.T, store *memStore, scope, bot, subject, body string,
) int64 {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextOut++
	id := store.nextOut
	store.outbox = append(store.outbox, queuedReply{
		scope: scope, bot: bot,
		entry: botflota.OutboxEntry{
			ID: id, Target: waTarget(subject), MessageID: 900 + id,
			Content: botflota.TextContent{Body: body},
			Origin:  botflota.OriginAPI, Routing: botflota.RoutingUnknown,
		},
	})
	return id
}

// TestAnAPISendReachesAHeldChat: a human hold suppresses the bot's own replies,
// but an API-origin send queued after the hold is delivered and the hold still
// stands (ADR 0043, ADR 0044).
func TestAnAPISendReachesAHeldChat(t *testing.T) {
	runner, channels, store := buildFleetWithStore(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()
	waitConnected(t, runner)
	alpha := channels["alpha"]

	if err := runner.SetChatHuman(ctx, "dev", "alpha", waTarget("held"), true); err != nil {
		t.Fatalf("SetChatHuman: %v", err)
	}
	queueAPISend(t, store, "dev", "alpha", "held", "aviso")
	sent := receive(t, alpha.sent)
	if body := botflota.ContentText(sent.Content); body != "aviso" {
		t.Fatalf("sent = %q, want the API send", body)
	}
	if sent.Origin != botflota.OriginAPI || sent.Target != waTarget("held") {
		t.Fatalf("sent = %#v", sent)
	}
	if state, ok := runner.ChatControl("dev", "alpha", waTarget("held")); !ok ||
		state.Mode != botflota.ChatHuman {
		t.Fatalf("control = %+v, %v; want the hold to stand", state, ok)
	}
}

// TestHeldInboundIsRecordedWithoutASession: while a chat is held, the fleet
// stores each inbound message for history and deduplication, runs no
// transition, and queues no reply; releasing the hold restores the bot
// (ADR 0043).
func TestHeldInboundIsRecordedWithoutASession(t *testing.T) {
	runner, channels, store := buildFleetWithStore(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()
	waitConnected(t, runner)
	alpha := channels["alpha"]

	if err := runner.SetChatHuman(ctx, "dev", "alpha", waTarget("held"), true); err != nil {
		t.Fatalf("SetChatHuman: %v", err)
	}
	alpha.incoming <- botflota.IncomingMessage{
		Bot: "alpha", Sender: waKey("held"), Target: waTarget("held"),
		Content:           botflota.TextContent{Body: "hola"},
		ProviderMessageID: "held-1", At: t0,
	}

	var stored *botflota.Message
	deadline := time.Now().Add(2 * time.Second)
	for stored == nil {
		store.mu.Lock()
		for _, message := range store.messages {
			if message.ProviderMessageID == "held-1" {
				copied := *message
				stored = &copied
				break
			}
		}
		store.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("the held inbound message was not recorded")
		}
		time.Sleep(time.Millisecond)
	}
	if stored.Direction != botflota.Inbound || stored.Target != waTarget("held") {
		t.Fatalf("held message = %+v", stored)
	}
	store.mu.Lock()
	sessions, queued := len(store.sessions), len(store.outbox)
	store.mu.Unlock()
	if sessions != 0 {
		t.Fatalf("sessions = %d, want none for a held chat", sessions)
	}
	if queued != 0 {
		t.Fatalf("queued replies = %d, want none for a held chat", queued)
	}
	select {
	case reply := <-alpha.sent:
		t.Fatalf("a held chat was answered: %#v", reply)
	case <-time.After(30 * time.Millisecond):
	}

	if err := runner.SetChatHuman(ctx, "dev", "alpha", waTarget("held"), false); err != nil {
		t.Fatalf("release: %v", err)
	}
	alpha.incoming <- botflota.IncomingMessage{
		Bot: "alpha", Sender: waKey("held"), Target: waTarget("held"),
		Content:           botflota.TextContent{Body: "again"},
		ProviderMessageID: "held-2", At: t0,
	}
	if reply := receive(t, alpha.sent); botflota.ContentText(reply.Content) != "hello from alpha" {
		t.Fatalf("reply after release = %q", botflota.ContentText(reply.Content))
	}
}

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
		Bot: "alpha", Sender: waKey("held"), Target: waTarget("held"),
		Content: botflota.TextContent{Body: "hi"},
	}
	<-alpha.entered
	inFlight := queuedReplyIDFor(t, store, "dev", "alpha", "held")

	if err := runner.SetChatHuman(ctx, "dev", "alpha", waTarget("held"), true); err != nil {
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
