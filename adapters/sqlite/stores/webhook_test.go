package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func TestWebhookQueueLifecycle(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	delivery := botflota.WebhookDelivery{
		EventID: "event-1", DeliveryID: "delivery-1",
		URL: "https://bardo.example/events", Event: botflota.EventSenderFirstSeen,
		Scope: "bardo", Payload: []byte(`{"id":"event-1"}`),
	}
	if err := db.Webhooks().Enqueue(ctx, &delivery); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// The same event for the same target is already queued.
	if err := db.Webhooks().Enqueue(ctx, &delivery); err != nil {
		t.Fatalf("repeat Enqueue: %v", err)
	}

	pending, err := db.Webhooks().NextPending(ctx, t0, 10)
	if err != nil {
		t.Fatalf("NextPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	entry := pending[0]
	if entry.EventID != "event-1" || entry.DeliveryID != "delivery-1" ||
		entry.URL != "https://bardo.example/events" ||
		entry.Event != botflota.EventSenderFirstSeen ||
		string(entry.Payload) != `{"id":"event-1"}` || entry.Attempts != 0 {
		t.Fatalf("entry = %#v", entry)
	}

	// A retry schedules the delivery and keeps it out of the due set until
	// its next attempt.
	if err := db.Webhooks().MarkRetry(ctx, entry.ID, "status 500", t1, t0); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	due, err := db.Webhooks().NextPending(ctx, t0, 10)
	if err != nil {
		t.Fatalf("NextPending before the retry: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("scheduled delivery is due early: %#v", due)
	}
	due, err = db.Webhooks().NextPending(ctx, t1, 10)
	if err != nil {
		t.Fatalf("NextPending at the retry: %v", err)
	}
	if len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("due = %#v, want the scheduled attempt", due)
	}

	if err := db.Webhooks().MarkSent(ctx, entry.ID, t1); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	due, err = db.Webhooks().NextPending(ctx, t1, 10)
	if err != nil {
		t.Fatalf("NextPending after sent: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("a sent delivery is still pending: %#v", due)
	}
	if err := db.Webhooks().MarkSent(ctx, entry.ID, t1); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("MarkSent on a final row = %v, want ErrNotFound", err)
	}
}

func TestWebhookQueueKeysByEventAndTarget(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, delivery := range []botflota.WebhookDelivery{
		{EventID: "e1", DeliveryID: "d1", URL: "https://a.test/hook",
			Event: botflota.EventSenderFirstSeen, Scope: "s"},
		{EventID: "e2", DeliveryID: "d2", URL: "https://a.test/hook",
			Event: botflota.EventSenderFirstSeen, Scope: "s"},
		{EventID: "e1", DeliveryID: "d3", URL: "https://b.test/hook",
			Event: botflota.EventSenderFirstSeen, Scope: "s"},
	} {
		if err := db.Webhooks().Enqueue(ctx, &delivery); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	pending, err := db.Webhooks().NextPending(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("NextPending: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("pending = %d, want 3 (one per event and target)", len(pending))
	}
}
