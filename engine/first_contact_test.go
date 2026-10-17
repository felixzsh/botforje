package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/engine"
)

// hookNotifier records the first-contact events an engine reports.
type hookNotifier struct {
	mu     sync.Mutex
	events []botflota.PeerFirstSeen
	err    error
}

func (h *hookNotifier) PeerFirstSeen(
	_ context.Context, event botflota.PeerFirstSeen,
) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
	return h.err
}

func (h *hookNotifier) recorded() []botflota.PeerFirstSeen {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]botflota.PeerFirstSeen(nil), h.events...)
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
		event.Peer.Key.Subject != sender || !event.At.Equal(t0) {
		t.Fatalf("event = %#v", event)
	}
}

// TestFirstContactFiresForAHeldChat: a message stored while a human holds the
// chat is still a first contact (ADR 0043, ADR 0045).
func TestFirstContactFiresForAHeldChat(t *testing.T) {
	hook := &hookNotifier{}
	eng := fixtureEngineNotify(t, newMemStore(), hook)
	_, err := eng.RecordSuppressed(context.Background(), botflota.IncomingMessage{
		Bot: "support-bot", Sender: waKey("5215500000002"), Target: waTarget("5215500000002"), At: t0,
		Content: botflota.TextContent{Body: "hola"},
	})
	if err != nil {
		t.Fatalf("RecordSuppressed: %v", err)
	}
	if events := hook.recorded(); len(events) != 1 ||
		events[0].Peer.Key.Subject != "5215500000002" {
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

// flakyStore breaks the chat or the session load after the contact was
// recorded, which is where the hook must not depend on either of them.
type flakyStore struct {
	*memStore
	breakChat    bool
	breakSession bool
}

func (f *flakyStore) Ensure(
	ctx context.Context, scope, bot string, target botflota.ChatTarget,
	peerID int64, at time.Time,
) (*botflota.Chat, error) {
	if f.breakChat {
		return nil, errors.New("chat store down")
	}
	return f.memStore.Ensure(ctx, scope, bot, target, peerID, at)
}

func (f *flakyStore) Latest(
	ctx context.Context, chatID int64,
) (*botflota.Session, error) {
	if f.breakSession {
		return nil, errors.New("session store down")
	}
	return f.memStore.Latest(ctx, chatID)
}

// TestFirstContactFiresBeforeTheChatAndSessionLoad: the hook is the claim of
// first_seen, not the success of the load that follows it, so a failure to
// read the chat or the session costs the turn but never the event, and the
// claim stays taken (ADR 0045).
func TestFirstContactFiresBeforeTheChatAndSessionLoad(t *testing.T) {
	for _, tc := range []struct {
		name         string
		breakChat    bool
		breakSession bool
	}{
		{"chat load fails", true, false},
		{"session load fails", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			hook := &hookNotifier{}
			contacts := newMemStore()
			store := &flakyStore{
				memStore: contacts, breakChat: tc.breakChat,
				breakSession: tc.breakSession,
			}
			scope := loadScope(t)
			eng := newTestEngine(t, engine.Options{
				Bot:            "support-bot",
				Scope:          "support",
				Graph:          scope.Graphs["faq"],
				Actions:        scope.Actions,
				Store:          store,
				Chats:          store,
				Peers:          contacts,
				Messages:       contacts,
				Turn:           contacts,
				Notify:         hook,
				DefaultTimeout: 300 * time.Second,
			})
			phone := "5215500000004"
			_, _, err := eng.Handle(ctx, botflota.IncomingMessage{
				Bot: "support-bot", Sender: waKey(phone), Target: waTarget(phone), At: t0,
				Content: botflota.TextContent{Body: "hola"},
			})
			if err == nil {
				t.Fatal("Handle must fail when the load fails")
			}
			if events := hook.recorded(); len(events) != 1 ||
				events[0].Peer.Key.Subject != phone {
				t.Fatalf("events = %#v, want the hook fired before the load", events)
			}
			// The claim stayed taken by the failed turn: the next message never
			// reports a second first contact.
			if _, isFirst, err := contacts.RecordInbound(ctx, "support",
				waKey(phone), "", t0.Add(time.Minute)); err != nil || isFirst {
				t.Fatalf("later inbound = (first %v, err %v), want (false, nil)",
					isFirst, err)
			}
		})
	}
}
