package fleet

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
)

const (
	// defaultOutboxPoll is the fallback read when no wake signal arrives;
	// it also picks up replies queued before a restart.
	defaultOutboxPoll  = time.Second
	defaultSendTimeout = 30 * time.Second
	defaultMaxAttempts = 5
	// defaultRetryBase doubles per attempt, capped at one minute, so a
	// temporarily rejected send is not hammered and a banned account is not
	// poked (ADR 0011).
	defaultRetryBase = 2 * time.Second
	maxRetryDelay    = time.Minute
)

type outboxSender func(ctx context.Context, msg botflota.OutgoingMessage) (string, error)

// Only Store, Scope, Bot, and Sender are required.
type outboxConfig struct {
	Store        botflota.OutboxStore
	Scope        string
	Bot          string
	Sender       outboxSender
	Delay        time.Duration
	Jitter       time.Duration
	MaxPerMinute int
	MaxAttempts  int
	SendTimeout  time.Duration
	Poll         time.Duration
	RetryBase    time.Duration
	Now          func() time.Time
	Rand         *rand.Rand
	Sleep        func(ctx context.Context, d time.Duration) error
	OnError      func(msg botflota.OutgoingMessage, err error)
	Log          *slog.Logger
}

// outbox delivers one bot's queued replies from the durable queue: the engine
// commits a reply with the turn it answers, and this worker paces, retries,
// and records the outcome, so a crash loses neither the reply nor its status
// (ADR 0011).
type outbox struct {
	cfg  outboxConfig
	wake chan struct{}
	// closing is closed by Close; an empty queue then ends the worker.
	closing   chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	// windowStart and count pace max_messages_per_minute (ADR 0011).
	mu          sync.Mutex
	windowStart time.Time
	count       int
}

func newOutbox(cfg outboxConfig) *outbox {
	if cfg.Poll <= 0 {
		cfg.Poll = defaultOutboxPoll
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = defaultSendTimeout
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = defaultRetryBase
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
		cfg:     cfg,
		wake:    make(chan struct{}, 1),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go q.run()
	return q
}

// Wake asks the worker to read the queue now; a queued reply does not wait
// for the poll. A wake while one is pending is coalesced.
func (q *outbox) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Close stops the worker after it drains what is already queued, so shutdown
// keeps the promise of ADR 0011.
func (q *outbox) Close() { q.closeOnce.Do(func() { close(q.closing) }) }

func (q *outbox) Wait() { <-q.done }

// Depth reports the replies still waiting. It reads the queue directly: a
// status question deserves the stored truth, and the driver bounds the query
// with its busy timeout.
func (q *outbox) Depth() int {
	depth, err := q.cfg.Store.Depth(context.Background(), q.cfg.Scope, q.cfg.Bot)
	if err != nil {
		q.cfg.Log.Warn("outbox depth", "error", err)
		return 0
	}
	return depth
}

func (q *outbox) run() {
	defer close(q.done)
	ticker := time.NewTicker(q.cfg.Poll)
	defer ticker.Stop()
	var last time.Time
	for {
		entry, err := q.next()
		if err != nil {
			q.cfg.Log.Error("outbox read failed", "error", err)
			select {
			case <-q.closing:
				return
			case <-ticker.C:
			}
			continue
		}
		if entry == nil {
			if q.isClosing() {
				return
			}
			select {
			case <-q.wake:
			case <-q.closing:
			case <-ticker.C:
			}
			continue
		}
		if !last.IsZero() {
			pacing := q.cfg.Delay + q.jitter()
			if pacing > 0 {
				_ = q.cfg.Sleep(context.Background(), pacing)
			}
		}
		q.waitRate()
		last = q.cfg.Now()
		q.send(*entry)
	}
}

func (q *outbox) next() (*botflota.OutboxEntry, error) {
	entries, err := q.cfg.Store.NextPending(context.Background(),
		q.cfg.Scope, q.cfg.Bot, q.cfg.Now(), 1)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return &entries[0], nil
}

func (q *outbox) isClosing() bool {
	select {
	case <-q.closing:
		return true
	default:
		return false
	}
}

func (q *outbox) jitter() time.Duration {
	if q.cfg.Jitter <= 0 {
		return 0
	}
	return time.Duration(q.cfg.Rand.Int63n(int64(q.cfg.Jitter) + 1))
}

// The ceiling uses a fixed one-minute window, not a sliding one.
func (q *outbox) waitRate() {
	if q.cfg.MaxPerMinute <= 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
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

// retryDelay backs off per attempt and stays bounded, so giving up is a
// matter of minutes, not hours.
func (q *outbox) retryDelay(attempts int) time.Duration {
	delay := q.cfg.RetryBase
	for i := 1; i < attempts && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	return delay
}

// send is one delivery attempt; its outcome is recorded on the queue entry,
// never only in a log (ADR 0011).
func (q *outbox) send(entry botflota.OutboxEntry) {
	if q.cfg.Sender == nil {
		return
	}
	msg := botflota.OutgoingMessage{
		Bot: q.cfg.Bot, RecipientPhone: entry.Recipient, Content: entry.Content,
		Origin: entry.Origin, Routing: entry.Routing,
		StoredMessageID: entry.MessageID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), q.cfg.SendTimeout)
	defer cancel()
	providerID, err := q.cfg.Sender(ctx, msg)
	attempts := entry.Attempts + 1
	if err == nil {
		if markErr := q.cfg.Store.MarkSent(context.Background(),
			entry.ID, providerID, q.cfg.Now()); markErr != nil {
			q.cfg.Log.Error("record sent reply", "error", markErr)
		}
		q.cfg.Log.Info("sent",
			"recipient", recipient(msg),
			"kind", string(botflota.ContentKind(msg.Content)),
			"attempts", attempts)
		return
	}
	if q.cfg.OnError != nil {
		q.cfg.OnError(msg, err)
	}
	if attempts >= q.cfg.MaxAttempts {
		q.cfg.Log.Error("send failed; giving up",
			"recipient", recipient(msg),
			"kind", string(botflota.ContentKind(msg.Content)),
			"attempts", attempts, "error", err)
		if markErr := q.cfg.Store.MarkFailed(context.Background(),
			entry.ID, err.Error(), q.cfg.Now()); markErr != nil {
			q.cfg.Log.Error("record failed reply", "error", markErr)
		}
		return
	}
	next := q.cfg.Now().Add(q.retryDelay(attempts))
	q.cfg.Log.Warn("send failed; retrying",
		"recipient", recipient(msg),
		"kind", string(botflota.ContentKind(msg.Content)),
		"attempts", attempts, "retry_in", next.Sub(q.cfg.Now()), "error", err)
	if markErr := q.cfg.Store.MarkRetry(context.Background(),
		entry.ID, err.Error(), next, q.cfg.Now()); markErr != nil {
		q.cfg.Log.Error("record retry", "error", markErr)
	}
}

// recipient is the phone number for logs; it never logs a LID.
func recipient(msg botflota.OutgoingMessage) string {
	if msg.RecipientPhone != "" {
		return msg.RecipientPhone
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
