package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// textFakeConfig is one bot on an explicitly selected text-only fake.
func textFakeConfig() *config.Config {
	return &config.Config{
		DefaultTimeout: 60,
		Scopes: map[string]spec.Scope{
			"dev": {
				Actions: map[string]spec.Action{"greet": {Steps: []spec.Step{
					{Message: &spec.Message{Body: spec.Text{"hi"}}},
				}}},
				Graphs: map[string]spec.Graph{"menu": {
					Root: "root",
					Nodes: map[string]spec.Node{
						"root": {OnEnter: []string{"greet"}},
					},
				}},
				Bots: map[string]spec.Bot{"demo": {
					Graph:   "menu",
					Channel: &spec.Channel{Provider: "fake", Mode: "text"},
				}},
			},
		},
	}
}

// TestTextOnlyFakeJoinsThroughRegistration proves a channel installs through a
// definition and its builder alone: no WhatsApp device store, no QR setup, and
// no adapter-specific settings reach the shared construction (ADR 0052).
func TestTextOnlyFakeJoinsThroughRegistration(t *testing.T) {
	var built *fakeChannel
	var boundScope, boundBot string
	var boundOptions any
	catalog, err := channels.New(channels.Definition{
		Provider: "fake", Kind: "fake", Modes: []string{"text"},
		Capabilities: channels.Capabilities{
			Send: []botflota.MessageKind{botflota.KindText},
		},
		Build: func(bind channels.Bind) (botflota.MessageChannel, error) {
			built = newFakeChannel()
			boundScope, boundBot, boundOptions = bind.Scope, bind.Bot, bind.Options
			return built, nil
		},
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	store := newStore()
	runner, err := New(textFakeConfig(), Options{
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: store, Outbox: store, Catalog: catalog,
		DefaultTimeout: time.Minute, Now: func() time.Time { return t0 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if built == nil {
		t.Fatal("the registered builder was never asked for a channel")
	}
	if boundScope != "dev" || boundBot != "demo" {
		t.Fatalf("bound %s/%s", boundScope, boundBot)
	}
	if boundOptions != nil {
		t.Fatalf("a definition without options must bind nil, got %#v", boundOptions)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	waitUntil(t, func() bool {
		built.mu.Lock()
		defer built.mu.Unlock()
		return built.connected
	}, "the registered channel connected")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// sharedBackend is one fake resource two logical channels are views of, the
// shape an adapter with a shared socket has (ADR 0052).
type sharedBackend struct {
	views int
}

// TestOneBackendServesTwoLogicalViews proves starting and stopping one logical
// channel leaves its peer usable, without a new common pool contract.
func TestOneBackendServesTwoLogicalViews(t *testing.T) {
	backend := &sharedBackend{}
	views := map[string]*fakeChannel{}
	catalog, err := channels.New(channels.Definition{
		Provider: "fake", Kind: "fake", Modes: []string{"text"},
		Capabilities: channels.Capabilities{
			Send: []botflota.MessageKind{botflota.KindText},
		},
		Build: func(bind channels.Bind) (botflota.MessageChannel, error) {
			backend.views++
			view := newFakeChannel()
			views[bind.Bot] = view
			return view, nil
		},
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	cfg := textFakeConfig()
	scope := cfg.Scopes["dev"]
	scope.Bots["peer"] = spec.Bot{
		Graph:   "menu",
		Channel: &spec.Channel{Provider: "fake", Mode: "text"},
	}
	cfg.Scopes["dev"] = scope

	store := newStore()
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: store, Outbox: store, Catalog: catalog,
		DefaultTimeout: time.Minute, Now: func() time.Time { return t0 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if backend.views != 2 {
		t.Fatalf("views = %d, want one per bot over the shared backend", backend.views)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	waitUntil(t, func() bool {
		views["demo"].mu.Lock()
		defer views["demo"].mu.Unlock()
		views["peer"].mu.Lock()
		defer views["peer"].mu.Unlock()
		return views["demo"].connected && views["peer"].connected
	}, "both views connected")

	if err := runner.StopBot("dev", "demo"); err != nil {
		t.Fatalf("StopBot: %v", err)
	}
	views["peer"].mu.Lock()
	peerConnected := views["peer"].connected
	views["peer"].mu.Unlock()
	if !peerConnected {
		t.Fatal("stopping one view disconnected its peer")
	}
}

func waitUntil(t *testing.T, ready func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ready() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
