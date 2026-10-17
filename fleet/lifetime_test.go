package fleet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// overlapSend records whether two sends ever ran at the same time, so a
// regression that gave one bot more than one outbox worker would show up.
type overlapSend struct {
	mu       sync.Mutex
	active   int
	overlaps int
	order    []string
}

func (s *overlapSend) send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	s.mu.Lock()
	s.active++
	if s.active > 1 {
		s.overlaps++
	}
	s.mu.Unlock()
	// Hold the send open long enough that a concurrent worker would overlap.
	time.Sleep(time.Millisecond)
	s.mu.Lock()
	s.order = append(s.order, botflota.ContentText(msg.Content))
	s.active--
	s.mu.Unlock()
	return "wamid." + botflota.ContentText(msg.Content), nil
}

func (s *overlapSend) overlapCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overlaps
}

func (s *overlapSend) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func seedOrderedReply(store *memStore, id int64, body string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextOut = max(store.nextOut, id)
	entry := p5aEntry(id, 0)
	entry.Content = botflota.TextContent{Body: body}
	store.outbox = append(store.outbox, queuedReply{
		scope: "dev", bot: "alpha", entry: entry,
	})
}

// TestOutboxSerializesSendsUnderConcurrentWakes: many goroutines may wake one
// bot's outbox and inbound reading runs beside it, but that bot still sends one
// reply at a time, in queue order (ADR 0011, ADR 0052).
func TestOutboxSerializesSendsUnderConcurrentWakes(t *testing.T) {
	store := newStore()
	const total = 10
	want := make([]string, 0, total)
	for i := 0; i < total; i++ {
		body := fmt.Sprintf("reply %d", i)
		seedOrderedReply(store, int64(i+1), body)
		want = append(want, body)
	}
	sender := &overlapSend{}
	cfg := p5aConfig(store, &scriptSend{}, time.Unix(100, 0))
	cfg.Send = sender.send
	queue := newOutbox(cfg)
	queue.Start()

	var wakers sync.WaitGroup
	for i := 0; i < 8; i++ {
		wakers.Add(1)
		go func() {
			defer wakers.Done()
			for j := 0; j < 40; j++ {
				queue.Wake()
			}
		}()
	}
	wakers.Wait()

	waitDepthZero(t, store)
	queue.Close()
	queue.Wait()

	if overlaps := sender.overlapCount(); overlaps != 0 {
		t.Fatalf("overlapping sends = %d, want one at a time", overlaps)
	}
	got := sender.sent()
	if len(got) != len(want) {
		t.Fatalf("sends = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("send order = %q, want %q", got, want)
		}
	}
}

// TestTransportFailureIsTransientNotAuthorizationLoss: a transport failure
// keeps the bot running and the entry retryable, and a shared backend outage
// stops none of its bindings; only an explicit authorization loss stops it with
// the queue pending (ADR 0052, ADR 0053).
func TestTransportFailureIsTransientNotAuthorizationLoss(t *testing.T) {
	store := newStore()
	seedP5A(store, 1, 0)
	sender := &scriptSend{script: []error{
		botflota.TransientSendFailure(errors.New("whatsapp: not connected"), 0),
	}}
	var mu sync.Mutex
	lost := 0
	cfg := p5aConfig(store, sender, time.Unix(100, 0))
	cfg.MaxAttempts = 3
	cfg.RetryBase = time.Millisecond
	cfg.OnAuthLost = func(botflota.OutboxEntry) {
		mu.Lock()
		lost++
		mu.Unlock()
	}
	queue := newOutbox(cfg)
	entries, err := store.NextPending(context.Background(), "dev", "alpha", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("NextPending = %v, %v", entries, err)
	}
	queue.send(entries[0])

	mu.Lock()
	defer mu.Unlock()
	if lost != 0 {
		t.Fatal("a transport failure was reported as lost authorization")
	}
	if queue.discard.Load() {
		t.Fatal("a transport failure stopped the worker")
	}
	state := queuedState(store, 1)
	if state.sent || state.failed {
		t.Fatalf("queued = %#v, want a retryable entry", state)
	}
	if state.entry.Attempts != 1 || state.nextAttempt.IsZero() {
		t.Fatalf("queued = %#v, want one attempt and a scheduled retry", state)
	}
}

// TestChannelOwnedStreamClosureEndsTheBot: the adapter owns its stream, so a
// closed inbound stream ends that bot cleanly and releases its channel while a
// peer bot keeps running (ADR 0011, ADR 0052).
func TestChannelOwnedStreamClosureEndsTheBot(t *testing.T) {
	runner, channels := buildFleet(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx) }()
	waitConnected(t, runner)

	close(channels["alpha"].incoming)
	deadline := time.Now().Add(2 * time.Second)
	for {
		stopped := false
		for _, status := range runner.Status() {
			if status.Bot == "alpha" && status.State == "stopped" {
				stopped = true
			}
		}
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the closed inbound stream did not end the bot")
		}
		time.Sleep(time.Millisecond)
	}
	if !channels["alpha"].wasDisconnected() {
		t.Fatal("the ended bot did not release its channel")
	}
	for _, status := range runner.Status() {
		if status.Bot == "beta" && status.State != "connected" {
			t.Fatalf("the peer bot = %q, want connected", status.State)
		}
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
