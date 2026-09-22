package fleet

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// seedReply queues one reply the way CommitTurn does, so the worker can be
// driven without an engine.
func seedReply(store *memStore, i int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextOut++
	store.outbox = append(store.outbox, queuedReply{
		scope: "dev", bot: "alpha",
		entry: botflota.OutboxEntry{
			ID: store.nextOut, Recipient: "521550000000" + strconv.Itoa(i),
			MessageID: store.nextOut,
			Content:   botflota.TextContent{Body: "reply " + strconv.Itoa(i)},
		},
	})
}

func waitDepthZero(t *testing.T, store *memStore) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		depth, err := store.Depth(context.Background(), "dev", "alpha")
		if err != nil {
			t.Fatalf("Depth: %v", err)
		}
		if depth == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the queue never drained")
}

// recordingSender fails its first failures calls and records every attempt.
type recordingSender struct {
	mu       sync.Mutex
	sent     []botflota.OutgoingMessage
	failures int
}

func (s *recordingSender) send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	if s.failures > 0 {
		s.failures--
		return "", errors.New("rejected")
	}
	return "wamid." + strconv.Itoa(len(s.sent)), nil
}

func (s *recordingSender) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *recordingSender) bodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	bodies := make([]string, 0, len(s.sent))
	for _, msg := range s.sent {
		bodies = append(bodies, botflota.ContentText(msg.Content))
	}
	return bodies
}

func workerConfig(store *memStore, sender outboxSender) outboxConfig {
	return outboxConfig{
		Store: store, Scope: "dev", Bot: "alpha",
		Sender: sender, Poll: 5 * time.Millisecond,
	}
}

func TestOutboxSendsQueuedRepliesInOrder(t *testing.T) {
	store := newStore()
	for i := 1; i <= 3; i++ {
		seedReply(store, i)
	}
	sender := &recordingSender{}
	queue := newOutbox(workerConfig(store, sender.send))

	waitDepthZero(t, store)
	queue.Close()
	queue.Wait()

	want := []string{"reply 1", "reply 2", "reply 3"}
	if got := sender.bodies(); !slices.Equal(got, want) {
		t.Fatalf("sent = %q, want %q", got, want)
	}
}

// TestOutboxDrainsAfterClose keeps the shutdown promise of ADR 0011 with the
// durable queue: Close ends the worker only after what is stored goes out.
func TestOutboxDrainsAfterClose(t *testing.T) {
	store := newStore()
	seedReply(store, 1)
	seedReply(store, 2)
	sender := &recordingSender{}
	queue := newOutbox(workerConfig(store, sender.send))
	queue.Close()

	done := make(chan struct{})
	go func() {
		queue.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker never finished draining")
	}
	if sender.attempts() != 2 {
		t.Fatalf("sent = %d, want both queued replies", sender.attempts())
	}
}

type clock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	if d > 0 {
		c.sleeps = append(c.sleeps, d)
	}
	return nil
}

func (c *clock) slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

func TestOutboxPacingWaitsBetweenReplies(t *testing.T) {
	store := newStore()
	seedReply(store, 1)
	seedReply(store, 2)
	clk := &clock{now: time.Unix(1, 0)}
	sender := &recordingSender{}
	cfg := workerConfig(store, sender.send)
	cfg.Delay = 10 * time.Millisecond
	cfg.Now = clk.Now
	cfg.Sleep = clk.Sleep
	queue := newOutbox(cfg)

	waitDepthZero(t, store)
	queue.Close()
	queue.Wait()

	if got := sender.bodies(); len(got) != 2 {
		t.Fatalf("sent = %q, want both replies", got)
	}
	if sleeps := clk.slept(); !slices.Contains(sleeps, 10*time.Millisecond) {
		t.Fatalf("sleeps = %v, want the inter-message delay", sleeps)
	}
}

func TestOutboxRateLimitWaitsTheWindow(t *testing.T) {
	store := newStore()
	for i := 1; i <= 3; i++ {
		seedReply(store, i)
	}
	clk := &clock{now: time.Unix(1, 0)}
	sender := &recordingSender{}
	cfg := workerConfig(store, sender.send)
	cfg.MaxPerMinute = 2
	cfg.Now = clk.Now
	cfg.Sleep = clk.Sleep
	queue := newOutbox(cfg)

	waitDepthZero(t, store)
	queue.Close()
	queue.Wait()

	if sender.attempts() != 3 {
		t.Fatalf("sent = %d, want all three", sender.attempts())
	}
	if sleeps := clk.slept(); !slices.Contains(sleeps, time.Minute) {
		t.Fatalf("sleeps = %v, want the one-minute ceiling wait", sleeps)
	}
}

// TestOutboxRetriesThenSends: a rejected send schedules the next attempt
// instead of vanishing into a log, and the reply ends sent with every
// attempt counted (ADR 0011).
func TestOutboxRetriesThenSends(t *testing.T) {
	store := newStore()
	seedReply(store, 1)
	sender := &recordingSender{failures: 2}
	cfg := workerConfig(store, sender.send)
	cfg.MaxAttempts = 5
	cfg.RetryBase = time.Millisecond
	queue := newOutbox(cfg)

	waitDepthZero(t, store)
	queue.Close()
	queue.Wait()

	if sender.attempts() != 3 {
		t.Fatalf("attempts = %d, want two failures and a success", sender.attempts())
	}
	store.mu.Lock()
	queued := store.outbox[0]
	store.mu.Unlock()
	if !queued.sent || queued.entry.Attempts != 3 {
		t.Fatalf("queued = %#v, want sent after three attempts", queued)
	}
}

// TestOutboxGivesUpAfterMaxAttempts: a reply that keeps failing ends failed
// and pending, so it neither retries forever nor blocks the ones behind it.
func TestOutboxGivesUpAfterMaxAttempts(t *testing.T) {
	store := newStore()
	seedReply(store, 1)
	sender := &recordingSender{failures: 100}
	var mu sync.Mutex
	var reported []error
	cfg := workerConfig(store, sender.send)
	cfg.MaxAttempts = 2
	cfg.RetryBase = time.Millisecond
	cfg.OnError = func(_ botflota.OutgoingMessage, err error) {
		mu.Lock()
		reported = append(reported, err)
		mu.Unlock()
	}
	queue := newOutbox(cfg)

	waitDepthZero(t, store)
	queue.Close()
	queue.Wait()

	if sender.attempts() != 2 {
		t.Fatalf("attempts = %d, want exactly the maximum", sender.attempts())
	}
	store.mu.Lock()
	queued := store.outbox[0]
	mu.Lock()
	failures := len(reported)
	mu.Unlock()
	store.mu.Unlock()
	if !queued.failed || queued.entry.Attempts != 2 {
		t.Fatalf("queued = %#v, want failed after two attempts", queued)
	}
	if failures != 2 {
		t.Fatalf("OnError calls = %d, want one per attempt", failures)
	}
}
