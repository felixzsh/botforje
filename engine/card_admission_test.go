package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/engine"
	"github.com/felixzsh/botflota/spec"
)

// cardDefinition is a text-and-card implementation that composes only phone
// entries, the way a text-only companion would declare its limits.
func cardDefinition() channels.Definition {
	return channels.Definition{
		Provider: "whatsapp",
		Kind:     "fake",
		Modes:    []string{"native"},
		Capabilities: channels.Capabilities{
			Send: []botflota.MessageKind{botflota.KindText, botflota.KindContact},
			CardVariants: []spec.ContactEntryKind{
				spec.ContactPhone,
			},
		},
		ValidateTarget: func(target botflota.ChatTarget) []string {
			if target.Provider != "whatsapp" {
				return []string{"target provider is not whatsapp"}
			}
			return nil
		},
	}
}

func cardEngine(
	t *testing.T, store *memStore, entryKind string,
) *engine.Engine {
	t.Helper()
	def := cardDefinition()
	admit := func(
		target botflota.ChatTarget, content botflota.SendableContent,
	) error {
		if reasons := channels.AdmitSend(def, target, content); len(reasons) > 0 {
			return fmt.Errorf("reply not admitted: %s", reasons[0])
		}
		return nil
	}
	graph := spec.Graph{
		Root:  "menu",
		Nodes: map[string]spec.Node{"menu": {OnEnter: []string{"share-card"}}},
	}
	actions := map[string]spec.Action{"share-card": {Steps: []spec.Step{{
		Contact: &spec.Contact{
			Name:    "Ana",
			Entries: []spec.ContactEntry{{Kind: entryKind, Value: "ana"}},
		},
	}}}}
	return newTestEngine(t, engine.Options{
		Bot: "support-bot", Scope: "support",
		Graph: graph, Actions: actions,
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: turnStore(store), Admit: admit,
		Bindings:       &admitBindings{generation: 3},
		DefaultTimeout: 300 * time.Second,
	})
}

func turnStore(store *memStore) *captureTurn { return &captureTurn{memStore: store} }

// TestEngineRefusesUnsupportedCardVariantsBeforeAdmission proves the shared
// admission gates an engine reply: a card variant the consuming bot cannot
// compose fails the turn before anything is queued or persisted — no silent
// downgrade to text, no wire send — while a declared variant is stored.
func TestEngineRefusesUnsupportedCardVariantsBeforeAdmission(t *testing.T) {
	for _, tc := range []struct {
		name      string
		entryKind string
		wantErr   bool
	}{
		{"declared phone variant is admitted", "phone", false},
		{"undeclared email variant fails", "email", true},
		{"undeclared address variant fails", "address", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			eng := cardEngine(t, store, tc.entryKind)
			_, _, err := eng.Handle(context.Background(),
				botflota.IncomingMessage{
					Bot:    "support-bot",
					Sender: waKey("card-1"), Target: waTarget("card-1"),
					At:      t0,
					Content: botflota.TextContent{Body: "hello"},
				})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "not admitted") {
					t.Fatalf("Handle err = %v, want the admission refusal", err)
				}
				if len(store.messages) != 0 {
					t.Fatalf("messages = %d, want nothing persisted",
						len(store.messages))
				}
				return
			}
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if len(store.messages) != 2 {
				t.Fatalf("messages = %d, want the inbound and the card",
					len(store.messages))
			}
			card, ok := store.messages[1].Content.(botflota.ContactContent)
			if !ok || len(card.Entries) != 1 || card.Entries[0].Kind != "phone" {
				t.Fatalf("reply = %#v, want the phone card", store.messages[1].Content)
			}
		})
	}
}
