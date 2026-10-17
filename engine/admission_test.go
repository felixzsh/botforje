package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/engine"
)

var errAdmitLimit = errors.New("reply not admitted: over the declared limit")

// admitBindings is a hand-written BindingStore: the generation it reports
// is the test's control of the committed binding.
type admitBindings struct {
	generation int64
	missing    bool
}

func (f *admitBindings) Get(
	_ context.Context, scope, bot string,
) (*botflota.Binding, error) {
	if f.missing {
		return nil, botflota.ErrNotFound
	}
	return &botflota.Binding{
		Scope: scope, Bot: bot, Provider: "whatsapp",
		Generation: f.generation,
	}, nil
}

func (f *admitBindings) List(_ context.Context) ([]botflota.Binding, error) {
	return nil, nil
}

func (f *admitBindings) Commit(_ context.Context, _ *botflota.Binding) error {
	return nil
}

func (f *admitBindings) Delete(_ context.Context, _, _ string) error {
	return nil
}

// captureTurn records the generation snapshot of every committed turn.
type captureTurn struct {
	*memStore
	generations []int64
}

func (c *captureTurn) CommitTurn(
	ctx context.Context, turn botflota.TurnRecord,
) error {
	c.generations = append(c.generations, turn.BindingGeneration)
	return c.memStore.CommitTurn(ctx, turn)
}

func admissionEngine(
	t *testing.T, store *memStore, turn botflota.TurnStore,
	admit func(botflota.ChatTarget, botflota.SendableContent) error,
	bindings botflota.BindingStore,
) *engine.Engine {
	t.Helper()
	scope := loadScope(t)
	return newTestEngine(t, engine.Options{
		Bot: "support-bot", Scope: "support",
		Graph: scope.Graphs["faq"], Actions: scope.Actions,
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: turn, Admit: admit, Bindings: bindings,
		DefaultTimeout: 300 * time.Second,
	})
}

// TestEngineAdmitsRepliesBeforeQueueAdmission: a rendered reply the
// consuming bot cannot deliver fails the turn before anything is persisted:
// no silent downgrade, no split, no new fallback (ADR 0052).
func TestEngineAdmitsRepliesBeforeQueueAdmission(t *testing.T) {
	limit := func(max int) func(
		botflota.ChatTarget, botflota.SendableContent,
	) error {
		return func(
			_ botflota.ChatTarget, content botflota.SendableContent,
		) error {
			if len(botflota.ContentText(content)) > max {
				return errAdmitLimit
			}
			return nil
		}
	}
	tests := []struct {
		name    string
		max     int
		wantErr bool
	}{
		// The "hello" reply renders 36 characters; the limit decides.
		{name: "declared limit admits the rendered payload", max: 1000},
		{
			name:    "rendered payload over the declared limit fails",
			max:     5,
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newMemStore()
			eng := admissionEngine(t, store, store,
				limit(test.max), &admitBindings{generation: 3})
			_, _, err := eng.Handle(context.Background(),
				botflota.IncomingMessage{
					Bot:    "support-bot",
					Sender: waKey("admit-1"), Target: waTarget("admit-1"),
					At:      t0,
					Content: botflota.TextContent{Body: "hello"},
				})
			if test.wantErr {
				if err == nil || !strings.Contains(
					err.Error(), "not admitted") {
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
			if len(store.messages) == 0 {
				t.Fatal("no messages stored for the admitted reply")
			}
		})
	}
}

// TestEngineSnapshotsBindingGeneration: the committed turn carries the
// generation the replies were admitted under (ADR 0040).
func TestEngineSnapshotsBindingGeneration(t *testing.T) {
	store := newMemStore()
	captured := &captureTurn{memStore: store}
	eng := admissionEngine(t, store, captured, nil,
		&admitBindings{generation: 7})
	_, _, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot:    "support-bot",
		Sender: waKey("admit-2"), Target: waTarget("admit-2"), At: t0,
		Content: botflota.TextContent{Body: "hello"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(captured.generations) != 1 || captured.generations[0] != 7 {
		t.Fatalf("generations = %v, want the admitted [7]", captured.generations)
	}
}

// TestEngineRefusesQueuedRepliesWithoutABinding: a new send without a
// committed binding is rejected instead of guessing a future one (ADR 0044).
func TestEngineRefusesQueuedRepliesWithoutABinding(t *testing.T) {
	store := newMemStore()
	eng := admissionEngine(t, store, store, nil, &admitBindings{missing: true})
	_, _, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot:    "support-bot",
		Sender: waKey("admit-3"), Target: waTarget("admit-3"), At: t0,
		Content: botflota.TextContent{Body: "hello"},
	})
	if err == nil || !strings.Contains(err.Error(), "no committed binding") {
		t.Fatalf("Handle err = %v, want the no-binding refusal", err)
	}
	if len(store.messages) != 0 {
		t.Fatalf("messages = %d, want nothing persisted", len(store.messages))
	}
}
