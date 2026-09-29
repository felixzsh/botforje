// Package webhook delivers Botflota's outbound events: a durable queue, one
// signed HTTP POST per configured target, and bounded retries (ADR 0045).
package webhook

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// Target is one configured webhook destination: where to deliver, the secret
// its body is signed with, and the event names it subscribes to.
type Target struct {
	URL    string
	Secret spec.Secret
	Events []string
}

// Targets resolves the instance configuration into delivery targets.
func Targets(settings []config.WebhookSettings) []Target {
	targets := make([]Target, 0, len(settings))
	for _, entry := range settings {
		targets = append(targets, Target{
			URL:    entry.URL,
			Secret: entry.Secret,
			Events: slices.Clone(entry.Events),
		})
	}
	return targets
}

// Sink implements botflota.FirstContactNotifier: it builds the event's
// envelope and queues one delivery per subscribed target. Queueing is one
// local insert per target; the HTTP call belongs to the worker (ADR 0045).
type Sink struct {
	store   botflota.WebhookStore
	targets []Target
	now     func() time.Time
}

func NewSink(store botflota.WebhookStore, targets []Target) *Sink {
	return &Sink{store: store, targets: targets, now: time.Now}
}

func (s *Sink) FirstContact(ctx context.Context, event botflota.FirstContact) error {
	subscribed := make([]Target, 0, len(s.targets))
	for _, target := range s.targets {
		if slices.Contains(target.Events, botflota.EventSenderFirstSeen) {
			subscribed = append(subscribed, target)
		}
	}
	if len(subscribed) == 0 {
		return nil
	}
	eventID, err := newUUID()
	if err != nil {
		return fmt.Errorf("webhook: event id: %w", err)
	}
	payload, err := json.Marshal(firstContactEnvelope{
		ID:         eventID,
		Event:      botflota.EventSenderFirstSeen,
		OccurredAt: formatTime(event.At),
		Scope:      event.Scope,
		Data: firstContactData{
			Bot: event.Bot,
			Sender: envelopeSender{
				Phone:       event.Sender.Phone,
				DisplayName: event.Sender.DisplayName,
			},
			FirstSeen: formatTime(event.Sender.FirstSeen),
		},
	})
	if err != nil {
		return fmt.Errorf("webhook: encode first contact: %w", err)
	}
	for _, target := range subscribed {
		deliveryID, err := newUUID()
		if err != nil {
			return fmt.Errorf("webhook: delivery id: %w", err)
		}
		if err := s.store.Enqueue(ctx, &botflota.WebhookDelivery{
			EventID: eventID, DeliveryID: deliveryID, URL: target.URL,
			Event: botflota.EventSenderFirstSeen, Scope: event.Scope,
			Payload: payload,
		}); err != nil {
			return err
		}
	}
	return nil
}

// firstContactEnvelope is the frozen wire shape of the sender.first_seen
// event, signed and sent exactly as it was queued (ADR 0045).
type firstContactEnvelope struct {
	ID         string           `json:"id"`
	Event      string           `json:"event"`
	OccurredAt string           `json:"occurred_at"`
	Scope      string           `json:"scope"`
	Data       firstContactData `json:"data"`
}

type firstContactData struct {
	Bot       string         `json:"bot"`
	Sender    envelopeSender `json:"sender"`
	FirstSeen string         `json:"first_seen"`
}

type envelopeSender struct {
	Phone       string `json:"phone"`
	DisplayName string `json:"display_name"`
}

// formatTime renders an event timestamp the way the engine does: RFC 3339 in
// UTC, empty for a zero time.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// newUUID returns a random RFC 4122 version-4 identifier, so events and
// deliveries carry ids a consumer can deduplicate on without a dependency.
func newUUID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]), nil
}
