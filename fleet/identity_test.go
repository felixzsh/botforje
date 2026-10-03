package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// aliasingConfig names two bots whose scope and bot keys join to the same text,
// so a registry keying on a joined string would hold one of them.
func aliasingConfig() *config.Config {
	graph := spec.Graph{
		Root:  "root",
		Nodes: map[string]spec.Node{"root": {Goal: "answer"}},
	}
	return &config.Config{
		Scopes: map[string]spec.Scope{
			"a/b": {
				Graphs: map[string]spec.Graph{"main": graph},
				Bots:   map[string]spec.Bot{"c": {Graph: "main"}},
			},
			"a": {
				Graphs: map[string]spec.Graph{"main": graph},
				Bots:   map[string]spec.Bot{"b/c": {Graph: "main"}},
			},
		},
	}
}

func TestBotRegistryKeepsCollidingNamesApart(t *testing.T) {
	store := newStore()
	runner, err := New(aliasingConfig(), Options{
		Store: store, Chats: store, Contacts: store, Messages: store,
		Turn: store, Outbox: store,
		NewChannel: func(BotSpec) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first, ok := runner.bot("a/b", "c")
	if !ok {
		t.Fatal("the first bot is missing from the registry")
	}
	second, ok := runner.bot("a", "b/c")
	if !ok {
		t.Fatal("the second bot is missing from the registry")
	}
	if first == second {
		t.Fatal("two bots whose names join to the same text share one entry")
	}
}

// TestSetChatHumanWaitsForATurnInFlight keeps a hold from racing a turn that is
// already past its pause check: the hold takes the bot's turn lock, so the turn
// commits before the hold applies (ADR 0043).
func TestSetChatHumanWaitsForATurnInFlight(t *testing.T) {
	store := newStore()
	runner := buildFleetWithChannel(t, store, newFakeChannel())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	waitConnected(t, runner)

	bot, ok := runner.bot("dev", "alpha")
	if !ok {
		t.Fatal("the bot is not in the registry")
	}
	bot.runtimeMu.Lock()
	holding := make(chan error, 1)
	go func() {
		holding <- runner.SetChatHuman(ctx, "dev", "alpha", "5215500000000", true)
	}()
	select {
	case err := <-holding:
		bot.runtimeMu.Unlock()
		t.Fatalf("the hold applied while a turn held the lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	bot.runtimeMu.Unlock()
	select {
	case err := <-holding:
		if err != nil {
			t.Fatalf("SetChatHuman: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the hold never applied")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
