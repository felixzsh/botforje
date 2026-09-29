package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/felixzsh/botflota"
)

// hookNotifier records the first-contact events an engine reports.
type hookNotifier struct {
	mu     sync.Mutex
	events []botflota.FirstContact
	err    error
}

func (h *hookNotifier) FirstContact(
	_ context.Context, event botflota.FirstContact,
) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
	return h.err
}

func (h *hookNotifier) recorded() []botflota.FirstContact {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]botflota.FirstContact(nil), h.events...)
}

// TestFirstContactFiresOncePerProfile: the hook is the created profile of the
// sender upsert, not every message (ADR 0029, ADR 0045).
func TestFirstContactFiresOncePerProfile(t *testing.T) {
	hook := &hookNotifier{}
	eng := fixtureEngineNotify(t, newMemStore(), hook)
	sender := "5215500000001"

	outbound(t, eng, sender, "hello")
	outbound(t, eng, sender, "1")

	events := hook.recorded()
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one", events)
	}
	event := events[0]
	if event.Scope != "support" || event.Bot != "support-bot" ||
		event.Sender.Phone != sender || !event.At.Equal(t0) {
		t.Fatalf("event = %#v", event)
	}
}

// TestFirstContactFiresForAHeldChat: a message stored while a human holds the
// chat is still a first contact (ADR 0043, ADR 0045).
func TestFirstContactFiresForAHeldChat(t *testing.T) {
	hook := &hookNotifier{}
	eng := fixtureEngineNotify(t, newMemStore(), hook)
	err := eng.RecordSuppressed(context.Background(), botflota.IncomingMessage{
		Bot: "support-bot", SenderPhone: "5215500000002", At: t0,
		Content: botflota.TextContent{Body: "hola"},
	})
	if err != nil {
		t.Fatalf("RecordSuppressed: %v", err)
	}
	if events := hook.recorded(); len(events) != 1 ||
		events[0].Sender.Phone != "5215500000002" {
		t.Fatalf("events = %#v", events)
	}
}

// TestFirstContactFailureDoesNotFailTheTurn: the profile is already stored, so
// a hook that cannot queue its event never costs the reply (ADR 0045).
func TestFirstContactFailureDoesNotFailTheTurn(t *testing.T) {
	hook := &hookNotifier{err: errors.New("queue down")}
	eng := fixtureEngineNotify(t, newMemStore(), hook)
	if got := outbound(t, eng, "5215500000003", "1"); len(got) == 0 {
		t.Fatal("the turn produced no reply after the hook failed")
	}
	if events := hook.recorded(); len(events) != 1 {
		t.Fatalf("events = %#v, want the hook to have been called", events)
	}
}
