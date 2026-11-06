package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/egress"
	"github.com/felixzsh/botflota/spec"
)

func TestOutboundClientFollowsTheInstanceEgressPolicy(t *testing.T) {
	t.Run("public_only refuses a loopback destination before dialing", func(t *testing.T) {
		client, err := outboundClient(&config.Config{Egress: "public_only"})
		if err != nil {
			t.Fatalf("outboundClient: %v", err)
		}
		_, err = client.Get("http://127.0.0.1:1/admin/reset")
		if !errors.Is(err, egress.ErrBlocked) {
			t.Fatalf("Get error = %v, want %v", err, egress.ErrBlocked)
		}
	})
	t.Run("no egress line keeps the standard client", func(t *testing.T) {
		client, err := outboundClient(&config.Config{})
		if err != nil {
			t.Fatalf("outboundClient: %v", err)
		}
		if client.Transport != nil {
			t.Fatalf("transport = %T, want the standard client", client.Transport)
		}
	})
	t.Run("an unknown mode is reported", func(t *testing.T) {
		if _, err := outboundClient(&config.Config{Egress: "private"}); err == nil {
			t.Fatal("outboundClient accepted an unknown egress mode")
		}
	})
}

// TestFleetOptionsCarryTheEgressClient proves the fleet `fleet run` assembles
// is built with the managed client, so a request step cannot silently fall back
// to the standard one (ADR 0048).
func TestFleetOptionsCarryTheEgressClient(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "botflota.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	catalog, err := pureCatalog()
	if err != nil {
		t.Fatalf("pure catalog: %v", err)
	}
	options, err := fleetOptions(
		&config.Config{Egress: "public_only"}, db, botflota.DiscardLogger(), catalog)
	if err != nil {
		t.Fatalf("fleetOptions: %v", err)
	}
	if options.HTTPClient == nil {
		t.Fatal("the fleet options carry no outbound client")
	}
	_, err = options.HTTPClient.Get("http://127.0.0.1:1/admin/reset")
	if !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("Get error = %v, want %v", err, egress.ErrBlocked)
	}
}

// TestDeliveryWorkerObeysTheEgressPolicy proves the webhook worker delivers
// through the instance client: a refused target fails with the refusal as its
// reason and never reaches the network (ADR 0048).
func TestDeliveryWorkerObeysTheEgressPolicy(t *testing.T) {
	target := newCountingServer(t)
	cfg := &config.Config{Egress: "public_only", Webhooks: []config.WebhookSettings{{
		URL:    target.URL,
		Secret: spec.NewSecret("secret"),
		Events: []string{"contact.first_seen"},
	}}}
	store := &fakeWebhookStore{
		pending: botflota.WebhookDelivery{
			ID: 1, DeliveryID: "d-1", URL: target.URL,
			Event: "contact.first_seen", Payload: []byte(`{"event":"contact.first_seen"}`),
		},
		attempts: make(chan string, 1),
	}
	worker, err := deliveryWorker(cfg, store, botflota.DiscardLogger())
	if err != nil {
		t.Fatalf("deliveryWorker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer worker.Wait()
	defer cancel()
	worker.Start(ctx)

	select {
	case reason := <-store.attempts:
		if !strings.Contains(reason, "failed: ") ||
			!strings.Contains(reason, egress.ErrBlocked.Error()) {
			t.Fatalf("recorded outcome = %q, want a permanent policy refusal", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the delivery was never attempted")
	}
	if got := target.requests(); got != 0 {
		t.Fatalf("the target received %d requests, want none", got)
	}
}

// fakeWebhookStore hands out one due delivery and records the outcome.
type fakeWebhookStore struct {
	mu       sync.Mutex
	pending  botflota.WebhookDelivery
	returned bool
	attempts chan string
}

func (s *fakeWebhookStore) Enqueue(context.Context, *botflota.WebhookDelivery) error {
	return nil
}

func (s *fakeWebhookStore) NextPending(
	context.Context, time.Time, int,
) ([]botflota.WebhookDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.returned {
		return nil, nil
	}
	s.returned = true
	return []botflota.WebhookDelivery{s.pending}, nil
}

func (s *fakeWebhookStore) MarkSent(context.Context, int64, time.Time) error {
	return nil
}

func (s *fakeWebhookStore) MarkRetry(
	_ context.Context, _ int64, reason string, _, _ time.Time,
) error {
	s.attempts <- "retry: " + reason
	return nil
}

func (s *fakeWebhookStore) MarkFailed(
	_ context.Context, _ int64, reason string, _ time.Time,
) error {
	s.attempts <- "failed: " + reason
	return nil
}

// countingServer records how many deliveries reached it.
type countingServer struct {
	*httptest.Server
	mu    sync.Mutex
	count int
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	server := &countingServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			server.mu.Lock()
			server.count++
			server.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
	t.Cleanup(server.Close)
	return server
}

func (s *countingServer) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}
