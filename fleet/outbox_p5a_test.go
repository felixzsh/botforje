package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// fakeBindings is a hand-written BindingStore for the delivery gate tests:
// the generation it reports is the test's control of detach and replacement.
type fakeBindings struct {
	mu       sync.Mutex
	bindings map[botflota.BotRef]*botflota.Binding
	err      error
}

func newFakeBindings() *fakeBindings {
	return &fakeBindings{bindings: map[botflota.BotRef]*botflota.Binding{}}
}

func (f *fakeBindings) Get(
	_ context.Context, scope, bot string,
) (*botflota.Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	binding, ok := f.bindings[botflota.BotRef{Scope: scope, Bot: bot}]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return binding, nil
}

func (f *fakeBindings) List(_ context.Context) ([]botflota.Binding, error) {
	return nil, nil
}

func (f *fakeBindings) Commit(_ context.Context, account *botflota.Binding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindings[botflota.BotRef{Scope: account.Scope, Bot: account.Bot}] = account
	return nil
}

func (f *fakeBindings) Delete(_ context.Context, scope, bot string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.bindings, botflota.BotRef{Scope: scope, Bot: bot})
	return nil
}

func (f *fakeBindings) set(scope, bot string, generation int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindings[botflota.BotRef{Scope: scope, Bot: bot}] = &botflota.Binding{
		Scope: scope, Bot: bot, Generation: generation,
	}
}

// scriptSend answers every attempt from a script and records the delivery
// key each attempt carried, so key reuse is observable.
type scriptSend struct {
	mu     sync.Mutex
	script []error
	ids    []string
	keys   []string
	calls  int
}

func (s *scriptSend) send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, msg.DeliveryKey)
	call := s.calls
	s.calls++
	if call < len(s.script) && s.script[call] != nil {
		return "", s.script[call]
	}
	id := fmt.Sprintf("wamid.%d", call+1)
	if call < len(s.ids) && s.ids[call] != "" {
		id = s.ids[call]
	}
	return id, nil
}

func (s *scriptSend) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func p5aEntry(id, generation int64) botflota.OutboxEntry {
	return botflota.OutboxEntry{
		ID: id, Target: botflota.ChatTarget{
			Provider: "whatsapp", Kind: botflota.ChatDirect,
			Thread: "5215500000007",
		},
		MessageID: id, Content: botflota.TextContent{Body: "reply"},
		BindingGeneration: generation, DeliveryKey: fmt.Sprintf("dlv-%d", id),
	}
}

func seedP5A(store *memStore, id, generation int64) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextOut = max(store.nextOut, id)
	store.outbox = append(store.outbox, queuedReply{
		scope: "dev", bot: "alpha", entry: p5aEntry(id, generation),
	})
}

func p5aConfig(
	store *memStore, sender *scriptSend, now time.Time,
) outboxConfig {
	return outboxConfig{
		Store: store, Scope: "dev", Bot: "alpha",
		Send: sender.send, Poll: 5 * time.Millisecond,
		Now: func() time.Time { return now },
	}
}

// drive sends the head entry until it leaves the queue or the test runs out
// of patience, refreshing the entry the way the worker does.
func drive(t *testing.T, queue *outbox, store *memStore) botflota.OutboxEntry {
	t.Helper()
	var last botflota.OutboxEntry
	for i := 0; i < 8; i++ {
		entries, err := store.NextPending(
			context.Background(), "dev", "alpha", 1)
		if err != nil {
			t.Fatalf("NextPending: %v", err)
		}
		if len(entries) == 0 {
			return last
		}
		last = entries[0]
		queue.send(entries[0])
	}
	t.Fatal("the entry never left the queue")
	return last
}

func queuedState(store *memStore, id int64) queuedReply {
	store.mu.Lock()
	defer store.mu.Unlock()
	return *store.findQueued(id)
}

// TestOutboxClassifiedOutcomes: permanent input fails immediately while
// transient failures retry within the bounded attempts (ADR 0052).
func TestOutboxClassifiedOutcomes(t *testing.T) {
	permanent := botflota.PermanentSendFailure(errors.New("unsupported"))
	transient := botflota.TransientSendFailure(errors.New("rejected"), 0)
	unsupported := fmt.Errorf("mail: %w", botflota.ErrUnsupportedKind)
	tests := []struct {
		name         string
		script       []error
		maxAttempts  int
		wantState    string
		wantAttempts int
		wantSends    int
	}{
		{
			name:         "permanent fails immediately",
			script:       []error{permanent, nil, nil, nil, nil},
			wantState:    "failed",
			wantAttempts: 1,
			wantSends:    1,
		},
		{
			name:         "unsupported kind fails immediately",
			script:       []error{unsupported, nil, nil, nil, nil},
			wantState:    "failed",
			wantAttempts: 1,
			wantSends:    1,
		},
		{
			name:         "transient retries then sends",
			script:       []error{transient, transient, nil},
			wantState:    "sent",
			wantAttempts: 3,
			wantSends:    3,
		},
		{
			name:         "transient gives up at the bound",
			script:       []error{transient, transient, transient, transient},
			maxAttempts:  2,
			wantState:    "failed",
			wantAttempts: 2,
			wantSends:    2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newStore()
			seedP5A(store, 1, 0)
			sender := &scriptSend{script: test.script}
			now := time.Unix(100, 0)
			cfg := p5aConfig(store, sender, now)
			if test.maxAttempts > 0 {
				cfg.MaxAttempts = test.maxAttempts
			}
			queue := newOutbox(cfg)
			drive(t, queue, store)
			state := queuedState(store, 1)
			switch test.wantState {
			case "failed":
				if !state.failed {
					t.Fatalf("queued = %#v, want failed", state)
				}
			case "sent":
				if !state.sent {
					t.Fatalf("queued = %#v, want sent", state)
				}
			}
			if state.entry.Attempts != test.wantAttempts {
				t.Fatalf("attempts = %d, want %d",
					state.entry.Attempts, test.wantAttempts)
			}
			if sender.attempts() != test.wantSends {
				t.Fatalf("sends = %d, want %d",
					sender.attempts(), test.wantSends)
			}
		})
	}
}

// TestOutboxPermanentDoesNotBlockFIFO: a permanently failing head ends at
// once, so the reply behind it still goes out (ADR 0052).
func TestOutboxPermanentDoesNotBlockFIFO(t *testing.T) {
	store := newStore()
	seedP5A(store, 1, 0)
	seedP5A(store, 2, 0)
	sender := &scriptSend{
		script: []error{
			botflota.PermanentSendFailure(errors.New("unsupported")),
			nil,
		},
	}
	queue := newOutbox(p5aConfig(store, sender, time.Unix(100, 0)))
	queue.Start()
	waitDepthZero(t, store)
	queue.Close()
	queue.Wait()

	if sender.attempts() != 2 {
		t.Fatalf("sends = %d, want the failure and the reply behind it",
			sender.attempts())
	}
	if first := queuedState(store, 1); !first.failed {
		t.Fatalf("head = %#v, want failed at once", first)
	}
	if second := queuedState(store, 2); !second.sent {
		t.Fatalf("behind = %#v, want sent", second)
	}
}

// TestOutboxHonorsRetryHint: a transient retry hint replaces the fixed
// backoff for that attempt (ADR 0052).
func TestOutboxHonorsRetryHint(t *testing.T) {
	store := newStore()
	seedP5A(store, 1, 0)
	sender := &scriptSend{script: []error{
		botflota.TransientSendFailure(errors.New("slow down"), 90*time.Second),
	}}
	now := time.Unix(100, 0)
	queue := newOutbox(p5aConfig(store, sender, now))
	entries, err := store.NextPending(context.Background(), "dev", "alpha", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	queue.send(entries[0])
	scheduled, err := store.NextPending(
		context.Background(), "dev", "alpha", 1)
	if err != nil || len(scheduled) != 1 {
		t.Fatalf("a hinted retry left the queue: %v, %v", scheduled, err)
	}
	if want := now.Add(90 * time.Second); !scheduled[0].NextAttempt.Equal(want) {
		t.Fatalf("next attempt = %s, want the hinted %s",
			scheduled[0].NextAttempt, want)
	}
}

// TestOutboxAuthLossLeavesQueuePending: losing binding authorization stops
// the worker with the queue pending and visible, never failed or sent.
func TestOutboxAuthLossLeavesQueuePending(t *testing.T) {
	store := newStore()
	seedP5A(store, 1, 0)
	sender := &scriptSend{script: []error{
		botflota.AuthorizationLost(errors.New("revoked")),
	}}
	var mu sync.Mutex
	var lost []botflota.OutboxEntry
	cfg := p5aConfig(store, sender, time.Unix(100, 0))
	cfg.OnAuthLost = func(entry botflota.OutboxEntry) {
		mu.Lock()
		lost = append(lost, entry)
		mu.Unlock()
	}
	queue := newOutbox(cfg)
	queue.Start()
	queue.Wait()

	if sender.attempts() != 1 {
		t.Fatalf("sends = %d, want exactly the attempt that found it", sender.attempts())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lost) != 1 || lost[0].ID != 1 {
		t.Fatalf("auth loss reports = %#v, want the pending entry", lost)
	}
	state := queuedState(store, 1)
	if state.sent || state.failed {
		t.Fatalf("queued = %#v, want still pending", state)
	}
	depth, err := store.Depth(context.Background(), "dev", "alpha")
	if err != nil || depth != 1 {
		t.Fatalf("depth = %d, %v, want the pending entry visible", depth, err)
	}
	if !queue.discard.Load() {
		t.Fatal("the worker did not stop after authorization loss")
	}
}

// TestOutboxBindingGenerationGate: an entry accepted under another
// generation never sends, whether the binding was replaced or detached.
func TestOutboxBindingGenerationGate(t *testing.T) {
	tests := []struct {
		name      string
		entryGen  int64
		storedGen int64
		detached  bool
		wantSent  bool
	}{
		{name: "same generation sends", entryGen: 2, storedGen: 2, wantSent: true},
		{
			name:     "replaced generation never sends",
			entryGen: 1, storedGen: 2, wantSent: false,
		},
		{
			name:     "detached binding never sends",
			entryGen: 1, detached: true, wantSent: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newStore()
			seedP5A(store, 1, test.entryGen)
			sender := &scriptSend{}
			bindings := newFakeBindings()
			if !test.detached {
				bindings.set("dev", "alpha", test.storedGen)
			}
			cfg := p5aConfig(store, sender, time.Unix(100, 0))
			cfg.Bindings = bindings
			queue := newOutbox(cfg)
			entries, err := store.NextPending(
				context.Background(), "dev", "alpha", 1)
			if err != nil || len(entries) != 1 {
				t.Fatalf("NextPending = %v, %v", entries, err)
			}
			queue.send(entries[0])
			state := queuedState(store, 1)
			if test.wantSent {
				if !state.sent || sender.attempts() != 1 {
					t.Fatalf("queued = %#v, sends = %d, want sent once",
						state, sender.attempts())
				}
				return
			}
			if state.sent || sender.attempts() != 0 {
				t.Fatalf("queued = %#v, sends = %d, want failed without sending",
					state, sender.attempts())
			}
			if !state.failed || state.entry.Attempts != 1 {
				t.Fatalf("queued = %#v, want failed once", state)
			}
		})
	}
}

// TestOutboxDeliveryRecheck: an eligibility change since admission fails the
// queued message permanently, with no send and no useless retries.
func TestOutboxDeliveryRecheck(t *testing.T) {
	store := newStore()
	seedP5A(store, 1, 0)
	sender := &scriptSend{}
	cfg := p5aConfig(store, sender, time.Unix(100, 0))
	cfg.MaxAttempts = 5
	cfg.CheckDelivery = func(botflota.OutboxEntry) error {
		return errors.New("that channel cannot send image")
	}
	queue := newOutbox(cfg)
	entries, err := store.NextPending(context.Background(), "dev", "alpha", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	queue.send(entries[0])
	state := queuedState(store, 1)
	if !state.failed || state.entry.Attempts != 1 {
		t.Fatalf("queued = %#v, want failed without retries", state)
	}
	if sender.attempts() != 0 {
		t.Fatalf("sends = %d, want none", sender.attempts())
	}
}

// TestOutboxUncertainOutcome: a timeout after possible acceptance retries
// at-least-once and never records a proven rejection (ADR 0052).
func TestOutboxUncertainOutcome(t *testing.T) {
	store := newStore()
	seedP5A(store, 7, 0)
	sender := &scriptSend{script: []error{
		botflota.UncertainSendFailure(errors.New("timeout after send")),
		botflota.UncertainSendFailure(errors.New("timeout after send")),
	}}
	cfg := p5aConfig(store, sender, time.Unix(100, 0))
	cfg.MaxAttempts = 1
	cfg.RetryBase = time.Millisecond
	queue := newOutbox(cfg)

	entries, err := store.NextPending(context.Background(), "dev", "alpha", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	queue.send(entries[0])
	state := queuedState(store, 7)
	if state.sent || state.failed {
		t.Fatalf("queued = %#v, want pending for the at-least-once retry", state)
	}
	entries, err = store.NextPending(context.Background(), "dev", "alpha", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	queue.send(entries[0])
	state = queuedState(store, 7)
	if !state.failed || state.entry.Attempts != 2 {
		t.Fatalf("queued = %#v, want failed after the retry", state)
	}
	if !strings.Contains(state.reason, "acceptance not confirmed") {
		t.Fatalf("reason = %q, want acceptance-not-confirmed", state.reason)
	}
	sender.mu.Lock()
	keys := append([]string(nil), sender.keys...)
	sender.mu.Unlock()
	if len(keys) != 2 || keys[0] != "dlv-7" || keys[1] != "dlv-7" {
		t.Fatalf("delivery keys = %q, want the same key reused", keys)
	}
}

// TestOutboxAcceptanceRecordsProviderID: a proven acceptance ends sent with
// the channel's id on the stored message; the key still rode along.
func TestOutboxAcceptanceRecordsProviderID(t *testing.T) {
	store := newStore()
	seedP5A(store, 3, 0)
	sender := &scriptSend{}
	queue := newOutbox(p5aConfig(store, sender, time.Unix(100, 0)))
	entries, err := store.NextPending(context.Background(), "dev", "alpha", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	queue.send(entries[0])
	state := queuedState(store, 3)
	if !state.sent {
		t.Fatalf("queued = %#v, want sent", state)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, message := range store.messages {
		if message.ID == state.entry.MessageID &&
			message.ProviderMessageID != "wamid.1" {
			t.Fatalf("provider id = %q, want the accepted wamid.1",
				message.ProviderMessageID)
		}
	}
	if keys := sender.keys; len(keys) != 1 || keys[0] != "dlv-3" {
		t.Fatalf("delivery keys = %q, want the delivery-owned key", keys)
	}
}
