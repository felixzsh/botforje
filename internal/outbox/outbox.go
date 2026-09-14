// Package outbox paces one bot's outbound messages (ADR 0011): a per-bot FIFO
// with queue_delay plus random jitter, an optional per-minute ceiling, and
// backpressure when the buffer is full.
package outbox

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/felixzsh/botforje/internal/domain"
	"github.com/felixzsh/botforje/internal/logging"
)

// ErrClosed is returned by Push after Close.
var ErrClosed = errors.New("outbox: closed")

// Sender delivers one message; a channel adapter implements it.
type Sender func(ctx context.Context, msg domain.OutgoingMessage) error

// Config configures a Queue. Only Sender is required.
type Config struct {
	Sender       Sender
	Delay        time.Duration
	Jitter       time.Duration
	MaxPerMinute int
	Capacity     int
	SendTimeout  time.Duration
	Now          func() time.Time
	Rand         *rand.Rand
	Sleep        func(ctx context.Context, d time.Duration) error
	OnError      func(msg domain.OutgoingMessage, err error)
	Log          *slog.Logger
}

// Queue is a per-bot FIFO. Push blocks when the buffer is full.
type Queue struct {
	cfg       Config
	in        chan domain.OutgoingMessage
	done      chan struct{}
	closeOnce sync.Once

	mu          sync.Mutex
	depth       int
	windowStart time.Time
	count       int
}

// New builds a Queue and starts its send loop.
func New(cfg Config) *Queue {
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
		cfg.Log = logging.Discard()
	}
	q := &Queue{
		cfg:  cfg,
		in:   make(chan domain.OutgoingMessage, cfg.Capacity),
		done: make(chan struct{}),
	}
	go q.run()
	return q
}

// Push enqueues a message, blocking while the buffer is full.
func (q *Queue) Push(ctx context.Context, msg domain.OutgoingMessage) error {
	select {
	case q.in <- msg:
		q.mu.Lock()
		q.depth++
		q.mu.Unlock()
		return nil
	case <-q.done:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting messages; what is already queued still goes out.
func (q *Queue) Close() { q.closeOnce.Do(func() { close(q.in) }) }

// Wait blocks until the queue has drained.
func (q *Queue) Wait() { <-q.done }

// Depth is the number of messages not yet sent.
func (q *Queue) Depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.depth
}

func (q *Queue) run() {
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

func (q *Queue) jitter() time.Duration {
	if q.cfg.Jitter <= 0 {
		return 0
	}
	return time.Duration(q.cfg.Rand.Int63n(int64(q.cfg.Jitter) + 1))
}

// waitRate enforces max_messages_per_minute with a fixed one-minute window.
func (q *Queue) waitRate() {
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

func (q *Queue) send(msg domain.OutgoingMessage, pacing time.Duration) {
	if q.cfg.Sender == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), q.cfg.SendTimeout)
	defer cancel()
	err := q.cfg.Sender(ctx, msg)
	if err != nil {
		q.cfg.Log.Error("send failed",
			"recipient", recipient(msg), "kind", string(msg.Kind), "delay", pacing, "error", err)
		if q.cfg.OnError != nil {
			q.cfg.OnError(msg, err)
		}
		return
	}
	q.cfg.Log.Info("sent", "recipient", recipient(msg), "kind", string(msg.Kind), "delay", pacing)
}

// recipient is the phone number for logs; it never logs a LID.
func recipient(msg domain.OutgoingMessage) string {
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
