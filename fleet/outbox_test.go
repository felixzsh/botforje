package fleet

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func TestOrderAndDrain(t *testing.T) {
	var mu sync.Mutex
	var got []string
	sender := func(_ context.Context, msg botflota.OutgoingMessage) (string, error) {
		mu.Lock()
		got = append(got, msg.Body)
		mu.Unlock()
		return "", nil
	}
	queue := newOutbox(outboxConfig{Sender: sender})
	for _, body := range []string{"a", "b", "c"} {
		if err := queue.Push(context.Background(), botflota.OutgoingMessage{Body: body}); err != nil {
			t.Fatalf("Push(%s): %v", body, err)
		}
	}
	queue.Close()
	queue.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("order = %q, want [a b c]", got)
	}
}

// clock is an injected Now/Sleep that records waits and advances.
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

func noSender(context.Context, botflota.OutgoingMessage) (string, error) { return "", nil }

func TestPacing(t *testing.T) {
	clk := &clock{now: time.Unix(0, 0)}
	queue := newOutbox(outboxConfig{
		Sender: noSender, Delay: 10 * time.Millisecond, Now: clk.Now, Sleep: clk.Sleep,
	})
	_ = queue.Push(context.Background(), botflota.OutgoingMessage{Body: "a"})
	_ = queue.Push(context.Background(), botflota.OutgoingMessage{Body: "b"})
	queue.Close()
	queue.Wait()
	if !contains(clk.slept(), 10*time.Millisecond) {
		t.Fatalf("expected an inter-message delay, got %v", clk.slept())
	}
}

func TestRateLimit(t *testing.T) {
	clk := &clock{now: time.Unix(0, 0)}
	queue := newOutbox(outboxConfig{Sender: noSender, MaxPerMinute: 2, Now: clk.Now, Sleep: clk.Sleep})
	for i := 0; i < 3; i++ {
		_ = queue.Push(context.Background(), botflota.OutgoingMessage{Body: "x"})
	}
	queue.Close()
	queue.Wait()
	if !contains(clk.slept(), time.Minute) {
		t.Fatalf("expected a one-minute ceiling wait, got %v", clk.slept())
	}
}

func TestBackpressure(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sender := func(context.Context, botflota.OutgoingMessage) (string, error) {
		once.Do(func() { close(entered) })
		<-release
		return "", nil
	}
	queue := newOutbox(outboxConfig{Sender: sender, Capacity: 1})
	ctx := context.Background()
	if err := queue.Push(ctx, botflota.OutgoingMessage{Body: "1"}); err != nil {
		t.Fatalf("first Push: %v", err)
	}
	<-entered
	if err := queue.Push(ctx, botflota.OutgoingMessage{Body: "2"}); err != nil {
		t.Fatalf("second Push: %v", err)
	}
	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := queue.Push(blocked, botflota.OutgoingMessage{Body: "3"}); !errors.Is(
		err, context.DeadlineExceeded,
	) {
		t.Fatalf("Push on a full queue = %v, want a deadline", err)
	}
	close(release)
	queue.Close()
	queue.Wait()
}

func contains(list []time.Duration, want time.Duration) bool {
	for _, d := range list {
		if d == want {
			return true
		}
	}
	return false
}
