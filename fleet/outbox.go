package fleet

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
)

// errOutboxClosed is returned by push after close.
var errOutboxClosed = errors.New("outbox: closed")

// outboxSender delivers one message and returns the channel's message id; a
// channel adapter implements it.
type outboxSender func(ctx context.Context, msg botflota.OutgoingMessage) (string, error)

// outboxConfig configures an outbox. Only Sender is required.
type outboxConfig struct {
	Sender       outboxSender
	Delay        time.Duration
	Jitter       time.Duration
	MaxPerMinute int
	Capacity     int
	SendTimeout  time.Duration
	Now          func() time.Time
	Rand         *rand.Rand
	Sleep        func(ctx context.Context, d time.Duration) error
	OnError      func(msg botflota.OutgoingMessage, err error)
	// OnSent receives the channel message id of a delivered message, for
	// deduplication of the stored reply (ADR 0026).
	OnSent func(msg botflota.OutgoingMessage, providerID string)
	Log    *slog.Logger
}

// outbox paces one bot's outbound messages (ADR 0011): a per-bot FIFO with
// queue_delay plus random jitter, an optional per-minute ceiling, and
// backpressure when the buffer is full. Push blocks when it is full.
type outbox struct {
	cfg       outboxConfig
	in        chan botflota.OutgoingMessage
	done      chan struct{}
	closeOnce sync.Once

	mu          sync.Mutex
	depth       int
	windowStart time.Time
	count       int
}

// newOutbox builds an outbox and starts its send loop.
func newOutbox(cfg outboxConfig) *outbox {
	if cfg.Capacity <= 0 {
		cfg.Capacity = 64
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	if cfg.Log == nil {
		cfg.Log = botflota.DiscardLogger()
	}
	q := &outbox{
		cfg:  cfg,
		in:   make(chan botflota.OutgoingMessage, cfg.Capacity),
		done: make(chan struct{}),
	}
	go q.run()
	return q
}

// Push enqueues a message, blocking while the buffer is full.
func (q *outbox) Push(ctx context.Context, msg botflota.OutgoingMessage) error {
	select {
	case q.in <- msg:
		q.mu.Lock()
		q.depth++
		q.mu.Unlock()
		return nil
	case <-q.done:
		return errOutboxClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting messages; what is already queued still goes out.
func (q *outbox) Close() { q.closeOnce.Do(func() { close(q.in) }) }

// Wait blocks until the queue has drained.
func (q *outbox) Wait() { <-q.done }

// Depth is the number of messages not yet sent.
func (q *outbox) Depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.depth
}

func (q *outbox) run() {
	defer close(q.done)
	var last time.Time
	for msg := range q.in {
		var pacing time.Duration
		if !last.IsZero() {
			pacing = q.cfg.Delay + q.jitter()
			_ = q.cfg.Sleep(context.Background(), pacing)
		}
		q.waitRate()
		last = q.cfg.Now()
		q.send(msg, pacing)
		q.mu.Lock()
		q.depth--
		q.mu.Unlock()
	}
}

func (q *outbox) jitter() time.Duration {
	if q.cfg.Jitter <= 0 {
		return 0
	}
	return time.Duration(q.cfg.Rand.Int63n(int64(q.cfg.Jitter) + 1))
}

// waitRate enforces max_messages_per_minute with a fixed one-minute window.
func (q *outbox) waitRate() {
	if q.cfg.MaxPerMinute <= 0 {
		return
	}
	now := q.cfg.Now()
	if q.windowStart.IsZero() || now.Sub(q.windowStart) >= time.Minute {
		q.windowStart = now
		q.count = 0
	}
	if q.count >= q.cfg.MaxPerMinute {
		wait := q.windowStart.Add(time.Minute).Sub(now)
		q.cfg.Log.Debug("rate limited", "wait", wait)
		_ = q.cfg.Sleep(context.Background(), wait)
		q.windowStart = q.cfg.Now()
		q.count = 0
	}
	q.count++
}

func (q *outbox) send(msg botflota.OutgoingMessage, pacing time.Duration) {
	if q.cfg.Sender == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), q.cfg.SendTimeout)
	defer cancel()
	providerID, err := q.cfg.Sender(ctx, msg)
	if err != nil {
		q.cfg.Log.Error("send failed",
			"recipient", recipient(msg), "kind", string(botflota.ContentKind(msg.Content)),
			"queue_delay", pacing, "error", err)
		if q.cfg.OnError != nil {
			q.cfg.OnError(msg, err)
		}
		return
	}
	if providerID != "" && q.cfg.OnSent != nil {
		q.cfg.OnSent(msg, providerID)
	}
	q.cfg.Log.Info("sent",
		"recipient", recipient(msg), "kind", string(botflota.ContentKind(msg.Content)),
		"queue_delay", pacing)
}

// recipient is the phone number for logs; it never logs a LID.
func recipient(msg botflota.OutgoingMessage) string {
	if msg.Recipient != "" {
		return msg.Recipient
	}
	return "unresolved"
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
