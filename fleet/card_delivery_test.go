package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// cardCatalog installs one fake implementation that sends text and cards
// but composes only the given entry variants.
func cardCatalog(
	t *testing.T, variants ...spec.ContactEntryKind,
) *channels.Catalog {
	t.Helper()
	catalog, err := channels.New(channels.Definition{
		Provider: "whatsapp",
		Kind:     "fake",
		Modes:    []string{"native"},
		Capabilities: channels.Capabilities{
			Send: []botflota.MessageKind{
				botflota.KindText, botflota.KindContact,
			},
			CardVariants: variants,
		},
		ValidateTarget: func(target botflota.ChatTarget) []string {
			if target.Provider != "whatsapp" {
				return []string{"target provider is not whatsapp"}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New catalog: %v", err)
	}
	return catalog
}

func seedCard(
	store *memStore, id int64, entryKind string,
) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextOut = max(store.nextOut, id)
	store.outbox = append(store.outbox, queuedReply{
		scope: "dev", bot: "alpha",
		entry: botflota.OutboxEntry{
			ID: id,
			Target: botflota.ChatTarget{
				Provider: "whatsapp", Kind: botflota.ChatDirect,
				Thread: "5215500000007",
			},
			MessageID: id,
			Content: botflota.ContactContent{Name: "Ana", Entries: []botflota.ContactEntry{
				{Kind: entryKind, Value: "ana"},
			}},
			BindingGeneration: 1,
			DeliveryKey:       "dlv-card",
		},
	})
}

// TestDeliveryRechecksCardEligibility proves the delivery recheck applies
// the same capability check as queue admission through the installed
// definition: a card variant that lost eligibility fails permanently with
// no wire send and no silent downgrade, while a still-supported variant is
// delivered.
func TestDeliveryRechecksCardEligibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		variants []spec.ContactEntryKind
		wantSent bool
	}{
		{"supported phone variant is delivered", []spec.ContactEntryKind{spec.ContactPhone}, true},
		{"revoked variant fails without a send", []spec.ContactEntryKind{spec.ContactEmail}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore()
			seedCard(store, 1, "phone")
			sender := &scriptSend{}
			catalog := cardCatalog(t, tc.variants...)
			cfg := p5aConfig(store, sender, time.Unix(100, 0))
			cfg.CheckDelivery = deliveryCheck(catalog,
				spec.Channel{Provider: "whatsapp", Mode: "native"})
			queue := newOutbox(cfg)

			entries, err := store.NextPending(context.Background(), "dev", "alpha", 1)
			if err != nil || len(entries) != 1 {
				t.Fatalf("NextPending = %v, %v", entries, err)
			}
			queue.send(entries[0])
			state := queuedState(store, 1)
			if tc.wantSent {
				if !state.sent || sender.attempts() != 1 {
					t.Fatalf("queued = %#v, sends = %d; want sent once",
						state, sender.attempts())
				}
				return
			}
			if !state.failed || sender.attempts() != 0 {
				t.Fatalf("queued = %#v, sends = %d; want failed with no send",
					state, sender.attempts())
			}
		})
	}
}
