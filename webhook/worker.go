package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/egress"
)

const (
	defaultPoll      = time.Second
	defaultTimeout   = 10 * time.Second
	defaultAttempts  = 8
	defaultRetryBase = 5 * time.Second
	maxRetryDelay    = 15 * time.Minute
	deliveryBatch    = 10
)

// WorkerOptions tune the delivery loop; a zero value uses the defaults.
type WorkerOptions struct {
	Client    *http.Client
	Poll      time.Duration
	Timeout   time.Duration
	Attempts  int
	RetryBase time.Duration
	Now       func() time.Time
	Log       *slog.Logger
}

// Worker delivers queued webhooks: it reads the due rows, signs and POSTs
// each one, and records sent, retry, or failed. Delivery is at-least-once and
// never blocks the engine (ADR 0045).
type Worker struct {
	// secrets maps each target URL to its signing secret, resolved once from
	// the running configuration; no secret rests in the queue (ADR 0045).
	secrets   map[string]string
	store     botflota.WebhookStore
	client    *http.Client
	poll      time.Duration
	timeout   time.Duration
	attempts  int
	retryBase time.Duration
	now       func() time.Time
	log       *slog.Logger

	mu      sync.Mutex
	started bool
	done    chan struct{}
}

func NewWorker(
	store botflota.WebhookStore, targets []Target, opts WorkerOptions,
) *Worker {
	worker := &Worker{
		store: store, done: make(chan struct{}),
		client: opts.Client, poll: opts.Poll, timeout: opts.Timeout,
		attempts: opts.Attempts, retryBase: opts.RetryBase,
		now: opts.Now, log: opts.Log,
		secrets: map[string]string{},
	}
	if worker.client == nil {
		worker.client = &http.Client{}
	}
	if worker.poll <= 0 {
		worker.poll = defaultPoll
	}
	if worker.timeout <= 0 {
		worker.timeout = defaultTimeout
	}
	if worker.attempts <= 0 {
		worker.attempts = defaultAttempts
	}
	if worker.retryBase <= 0 {
		worker.retryBase = defaultRetryBase
	}
	if worker.now == nil {
		worker.now = time.Now
	}
	if worker.log == nil {
		worker.log = botflota.DiscardLogger()
	}
	for _, target := range targets {
		worker.secrets[target.URL] = target.Secret.Reveal()
	}
	return worker
}

// Start begins delivery once; the loop ends when ctx is cancelled. A worker
// that never started has nothing to wait for, so Wait only follows Start.
func (w *Worker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return
	}
	w.started = true
	go func() {
		defer close(w.done)
		w.run(ctx)
	}()
}

// Wait blocks until the delivery loop returned.
func (w *Worker) Wait() { <-w.done }

func (w *Worker) run(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		if err := w.deliverDue(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("webhook delivery pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) deliverDue(ctx context.Context) error {
	deliveries, err := w.store.NextPending(ctx, w.now(), deliveryBatch)
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		if ctx.Err() != nil {
			return nil
		}
		w.attempt(ctx, delivery)
	}
	return nil
}

// attempt makes one delivery: a 2xx ends it, a network failure or 429/5xx
// schedules a retry, and anything else fails it with the target's answer.
func (w *Worker) attempt(ctx context.Context, delivery botflota.WebhookDelivery) {
	secret, ok := w.secrets[delivery.URL]
	if !ok {
		w.fail(ctx, delivery, "target is no longer configured")
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		delivery.URL, bytes.NewReader(delivery.Payload))
	if err != nil {
		w.fail(ctx, delivery, err.Error())
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Botflota-Event", delivery.Event)
	request.Header.Set("X-Botflota-Delivery", delivery.DeliveryID)
	request.Header.Set("X-Botflota-Signature",
		"sha256="+signature(secret, delivery.Payload))

	attemptCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	response, err := w.client.Do(request.WithContext(attemptCtx))
	if err != nil {
		// A policy refusal is a decision that retrying cannot change, so the
		// target fails now with the refusal as its reason (ADR 0048); any other
		// failure stays worth another attempt.
		if errors.Is(err, egress.ErrBlocked) {
			w.fail(ctx, delivery, err.Error())
			return
		}
		w.retry(ctx, delivery, err.Error())
		return
	}
	defer response.Body.Close()
	// The body is drained so the connection can be reused; it never reaches
	// a log.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<10))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		if err := w.store.MarkSent(ctx, delivery.ID, w.now()); err != nil {
			w.log.Error("webhook mark sent", "id", delivery.ID, "error", err)
		}
		w.log.Info("webhook delivered", "url", delivery.URL,
			"event", delivery.Event, "delivery", delivery.DeliveryID)
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		w.retry(ctx, delivery, fmt.Sprintf("status %d", response.StatusCode))
	default:
		w.fail(ctx, delivery, fmt.Sprintf("status %d", response.StatusCode))
	}
}

func (w *Worker) retry(
	ctx context.Context, delivery botflota.WebhookDelivery, reason string,
) {
	attempts := delivery.Attempts + 1
	if attempts >= w.attempts {
		w.fail(ctx, delivery, reason)
		return
	}
	next := w.now().Add(w.retryDelay(attempts))
	if err := w.store.MarkRetry(ctx, delivery.ID, reason, next, w.now()); err != nil {
		w.log.Error("webhook mark retry", "id", delivery.ID, "error", err)
	}
	w.log.Warn("webhook retry", "url", delivery.URL, "event", delivery.Event,
		"attempts", attempts, "retry_in", next.Sub(w.now()), "error", reason)
}

func (w *Worker) fail(
	ctx context.Context, delivery botflota.WebhookDelivery, reason string,
) {
	if err := w.store.MarkFailed(ctx, delivery.ID, reason, w.now()); err != nil {
		w.log.Error("webhook mark failed", "id", delivery.ID, "error", err)
		return
	}
	w.log.Warn("webhook failed; giving up", "url", delivery.URL,
		"event", delivery.Event, "attempts", delivery.Attempts+1, "error", reason)
}

// retryDelay backs off per attempt and stays bounded (ADR 0045).
func (w *Worker) retryDelay(attempts int) time.Duration {
	delay := w.retryBase
	for i := 1; i < attempts && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	return delay
}

// signature is the HMAC-SHA256 of the raw body, hex-encoded; the consumer
// recomputes it over the bytes it received (ADR 0045).
func signature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
