package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

var t0 = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

// memQueue is a hand-written WebhookStore: rows keep their own state, so a
// test can watch a delivery move from pending to retry to sent or failed.
type memQueue struct {
	mu         sync.Mutex
	deliveries []*memDelivery
	nextID     int64
}

type memDelivery struct {
	delivery botflota.WebhookDelivery
	state    string // pending, sent, or failed
	reason   string
}

func (q *memQueue) Enqueue(
	_ context.Context, delivery *botflota.WebhookDelivery,
) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, existing := range q.deliveries {
		if existing.delivery.EventID == delivery.EventID &&
			existing.delivery.URL == delivery.URL {
			return nil
		}
	}
	q.nextID++
	stored := *delivery
	stored.ID = q.nextID
	if stored.DeliveryID == "" {
		stored.DeliveryID = fmt.Sprintf("delivery-%d", q.nextID)
	}
	q.deliveries = append(q.deliveries, &memDelivery{delivery: stored, state: "pending"})
	return nil
}

func (q *memQueue) NextPending(
	_ context.Context, at time.Time, limit int,
) ([]botflota.WebhookDelivery, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var due []botflota.WebhookDelivery
	for _, entry := range q.deliveries {
		if len(due) >= limit {
			break
		}
		if entry.state != "pending" ||
			(!entry.delivery.NextAttempt.IsZero() && entry.delivery.NextAttempt.After(at)) {
			continue
		}
		due = append(due, entry.delivery)
	}
	return due, nil
}

func (q *memQueue) MarkSent(_ context.Context, id int64, _ time.Time) error {
	return q.finalize(id, "sent", "")
}

func (q *memQueue) MarkRetry(
	_ context.Context, id int64, reason string, next, _ time.Time,
) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.find(id)
	if entry == nil || entry.state != "pending" {
		return botflota.ErrNotFound
	}
	entry.delivery.Attempts++
	entry.delivery.NextAttempt = next
	entry.reason = reason
	return nil
}

func (q *memQueue) MarkFailed(
	_ context.Context, id int64, reason string, _ time.Time,
) error {
	return q.finalize(id, "failed", reason)
}

func (q *memQueue) finalize(id int64, state, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.find(id)
	if entry == nil || entry.state != "pending" {
		return botflota.ErrNotFound
	}
	entry.state = state
	entry.reason = reason
	return nil
}

func (q *memQueue) find(id int64) *memDelivery {
	for _, entry := range q.deliveries {
		if entry.delivery.ID == id {
			return entry
		}
	}
	return nil
}

// entry returns a snapshot of the delivery with one event id.
func (q *memQueue) entry(eventID string) *memDelivery {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, entry := range q.deliveries {
		if entry.delivery.EventID == eventID {
			copied := *entry
			return &copied
		}
	}
	return nil
}

func (q *memQueue) all() []*memDelivery {
	q.mu.Lock()
	defer q.mu.Unlock()
	all := make([]*memDelivery, 0, len(q.deliveries))
	for _, entry := range q.deliveries {
		copied := *entry
		all = append(all, &copied)
	}
	return all
}

func TestSignatureIsHMACSHA256(t *testing.T) {
	got := signature("key", []byte("The quick brown fox jumps over the lazy dog"))
	want := "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	if got != want {
		t.Fatalf("signature = %s, want %s", got, want)
	}
}

// TestSinkQueuesOneDeliveryPerSubscribedTarget: the envelope is frozen once,
// the same event id travels to every target, and each delivery carries its
// own id (ADR 0045).
func TestSinkQueuesOneDeliveryPerSubscribedTarget(t *testing.T) {
	queue := &memQueue{}
	sink := NewSink(queue, []Target{
		{URL: "https://a.test/hook", Secret: spec.NewSecret("s1"),
			Events: []string{botflota.EventPeerFirstSeen}},
		{URL: "https://b.test/hook", Secret: spec.NewSecret("s2"),
			Events: []string{"other.event"}},
		{URL: "https://c.test/hook", Secret: spec.NewSecret("s3"),
			Events: []string{botflota.EventPeerFirstSeen}},
	})
	event := botflota.PeerFirstSeen{
		Scope: "bardo", Bot: "bardo",
		Peer: botflota.Peer{
			Key:         botflota.IdentityKey{Provider: "whatsapp", Subject: "5218994251984"},
			DisplayName: "Ana", FirstSeen: t0,
		},
		At: t0,
	}
	if err := sink.PeerFirstSeen(context.Background(), event); err != nil {
		t.Fatalf("PeerFirstSeen: %v", err)
	}
	stored := queue.all()
	if len(stored) != 2 {
		t.Fatalf("deliveries = %d, want one per subscribed target", len(stored))
	}

	var envelope map[string]any
	if err := json.Unmarshal(stored[0].delivery.Payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope["id"] == "" || envelope["event"] != botflota.EventPeerFirstSeen ||
		envelope["scope"] != "bardo" ||
		envelope["occurred_at"] != "2026-09-28T10:00:00Z" {
		t.Fatalf("envelope = %#v", envelope)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok || data["bot"] != "bardo" ||
		data["first_seen"] != "2026-09-28T10:00:00Z" {
		t.Fatalf("data = %#v", envelope["data"])
	}
	peer, ok := data["peer"].(map[string]any)
	if !ok || peer["subject"] != "5218994251984" ||
		peer["provider"] != "whatsapp" || peer["display_name"] != "Ana" {
		t.Fatalf("peer = %#v", data["peer"])
	}
	if stored[0].delivery.EventID != stored[1].delivery.EventID {
		t.Fatalf("event ids differ: %q vs %q",
			stored[0].delivery.EventID, stored[1].delivery.EventID)
	}
	if stored[0].delivery.DeliveryID == stored[1].delivery.DeliveryID {
		t.Fatalf("delivery ids are not distinct: %q",
			stored[0].delivery.DeliveryID)
	}
}

// TestWorkerSignsRetriesAndDelivers: a 5xx schedules a retry, the retry keeps
// the same delivery id, and the raw body verifies against the signature
// (ADR 0045).
func TestWorkerSignsRetriesAndDelivers(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	var body []byte
	var gotSignature, gotEvent, gotDelivery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		attempts++
		attempt := attempts
		body = raw
		gotSignature = r.Header.Get("X-Botflota-Signature")
		gotEvent = r.Header.Get("X-Botflota-Event")
		gotDelivery = r.Header.Get("X-Botflota-Delivery")
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	queue := &memQueue{}
	delivery := botflota.WebhookDelivery{
		EventID: "event-1", DeliveryID: "delivery-1", URL: server.URL,
		Event: botflota.EventPeerFirstSeen, Scope: "bardo",
		Payload: []byte(`{"id":"event-1","event":"peer.first_seen"}`),
	}
	if err := queue.Enqueue(context.Background(), &delivery); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var clock struct {
		mu sync.Mutex
		at time.Time
	}
	clock.at = t0
	now := func() time.Time {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		return clock.at
	}
	worker := NewWorker(queue,
		[]Target{{URL: server.URL, Secret: spec.NewSecret("top-secret")}},
		WorkerOptions{
			Now: now, Poll: time.Millisecond,
			RetryBase: time.Second, Attempts: 3,
		})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)

	waitFor(t, "the first attempt and a scheduled retry", func() bool {
		entry := queue.entry("event-1")
		return entry != nil && entry.state == "pending" &&
			entry.delivery.Attempts == 1 &&
			entry.delivery.NextAttempt.After(t0)
	})
	clock.mu.Lock()
	clock.at = t0.Add(time.Minute)
	clock.mu.Unlock()
	waitFor(t, "the retry to be delivered", func() bool {
		entry := queue.entry("event-1")
		return entry != nil && entry.state == "sent"
	})
	cancel()
	worker.Wait()

	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	mac := hmac.New(sha256.New, []byte("top-secret"))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSignature != want {
		t.Fatalf("signature = %q, want %q", gotSignature, want)
	}
	if gotEvent != "peer.first_seen" || gotDelivery != "delivery-1" {
		t.Fatalf("headers = %q/%q", gotEvent, gotDelivery)
	}
}

// TestWorkerGivesUpOnARejectedDelivery: a 4xx that is not 429 is permanent, so
// the delivery fails at once and the row keeps the reason (ADR 0045).
func TestWorkerGivesUpOnARejectedDelivery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	queue := &memQueue{}
	delivery := botflota.WebhookDelivery{
		EventID: "event-2", URL: server.URL,
		Event: botflota.EventPeerFirstSeen, Scope: "bardo",
		Payload: []byte(`{}`),
	}
	if err := queue.Enqueue(context.Background(), &delivery); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	worker := NewWorker(queue,
		[]Target{{URL: server.URL, Secret: spec.NewSecret("s")}},
		WorkerOptions{Poll: time.Millisecond, Attempts: 3})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	waitFor(t, "the rejected delivery to fail", func() bool {
		entry := queue.entry("event-2")
		return entry != nil && entry.state == "failed" &&
			entry.reason == "status 400"
	})
	cancel()
	worker.Wait()
}

// TestWorkerFailsADeliveryForAnUnconfiguredTarget: removing a target from the
// configuration fails its pending deliveries instead of holding them forever
// (ADR 0045).
func TestWorkerFailsADeliveryForAnUnconfiguredTarget(t *testing.T) {
	queue := &memQueue{}
	delivery := botflota.WebhookDelivery{
		EventID: "event-3", URL: "https://gone.test/hook",
		Event: botflota.EventPeerFirstSeen, Scope: "bardo",
		Payload: []byte(`{}`),
	}
	if err := queue.Enqueue(context.Background(), &delivery); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	worker := NewWorker(queue, nil, WorkerOptions{Poll: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	waitFor(t, "the delivery to fail", func() bool {
		entry := queue.entry("event-3")
		return entry != nil && entry.state == "failed" &&
			entry.reason == "target is no longer configured"
	})
	cancel()
	worker.Wait()
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
