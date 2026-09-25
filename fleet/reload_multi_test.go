package fleet

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// twoScopeCandidate is a candidate changing both configured scopes: "dev" with
// its own bot, and "other" cloned from it, so one reload carries two scopes the
// way a shared file edited by two editors would.
func twoScopeCandidate(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	other := cfg.Scopes["dev"]
	other.Bots = map[string]spec.Bot{"gamma": other.Bots["alpha"]}
	other.Actions = map[string]spec.Action{"greet": {Steps: []spec.Step{
		{Message: &spec.Message{Body: spec.Text{"hello from other"}}},
	}}}
	cfg.Scopes["other"] = other
	return cfg
}

// buildFleetWithClock is buildFleetWithStore with a clock the test moves, so a
// session's expiry can pass without waiting for it.
func buildFleetWithClock(
	t *testing.T, now func() time.Time,
) (*Fleet, map[string]*fakeChannel, *memStore) {
	t.Helper()
	cfg := twoScopeCandidate(t)
	if errs := config.Validate(cfg); len(errs) != 0 {
		t.Fatalf("config invalid: %v", errs)
	}
	channels := map[string]*fakeChannel{}
	store := newStore()
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Senders: store, Messages: store,
		Turn: store, Outbox: store, DefaultTimeout: time.Minute,
		Now: now,
		NewChannel: func(bot BotSpec) (botflota.MessageChannel, error) {
			channel := newFakeChannel()
			channels[bot.Scope+"/"+bot.Name] = channel
			return channel, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, channels, store
}

// TestApplyConfigAppliesAMultiScopeCandidate covers a reload that changes more
// than one scope at once: both become the running configuration in one call,
// and neither loses the other's change.
func TestApplyConfigAppliesAMultiScopeCandidate(t *testing.T) {
	runner, channels, _ := buildFleetWithClock(t, func() time.Time { return t0 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	// Both bots talk first, so each scope has live runtime state to replace.
	for _, target := range []struct{ scope, bot string }{
		{"dev", "alpha"}, {"other", "gamma"},
	} {
		channels[target.scope+"/"+target.bot].incoming <- botflota.IncomingMessage{
			Bot: target.bot, SenderPhone: "before-" + target.bot,
			Content: botflota.TextContent{Body: "hi"},
		}
		receive(t, channels[target.scope+"/"+target.bot].sent)
	}

	candidate := twoScopeCandidate(t)
	for _, scopeName := range []string{"dev", "other"} {
		scope := candidate.Scopes[scopeName]
		scope.Actions["greet"] = spec.Action{Steps: []spec.Step{
			{Message: &spec.Message{Body: spec.Text{"reloaded " + scopeName}}},
		}}
		candidate.Scopes[scopeName] = scope
	}
	applied, pending, err := runner.ApplyConfig(
		ctx, candidate, []string{"dev", "other"}, nil)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if len(pending) != 0 || !slices.Equal(applied, []string{"dev", "other"}) {
		t.Fatalf("ApplyConfig = %v, %v; want both scopes applied", applied, pending)
	}
	runner.mu.Lock()
	devGen, otherGen := runner.scopeGen["dev"], runner.scopeGen["other"]
	runner.mu.Unlock()
	if devGen != 1 || otherGen != 1 {
		t.Fatalf("scope generations = %d, %d; want both advanced", devGen, otherGen)
	}

	// Each scope runs its own reloaded action, so neither overwrote the other.
	for _, target := range []struct{ scope, bot string }{
		{"dev", "alpha"}, {"other", "gamma"},
	} {
		channels[target.scope+"/"+target.bot].incoming <- botflota.IncomingMessage{
			Bot: target.bot, SenderPhone: "after-" + target.bot,
			Content: botflota.TextContent{Body: "hi"},
		}
		reply := receive(t, channels[target.scope+"/"+target.bot].sent)
		if got, want := botflota.ContentText(reply.Content), "reloaded "+target.scope; got != want {
			t.Fatalf("%s reply = %q, want %q", target.scope, got, want)
		}
	}
}

// TestApplyConfigDefersUntilSessionsExpire covers the other half of a deferral:
// a graph change waits while a session is live, and the same candidate lands
// once that session expires, with no new edit and no restart (ADR 0009).
func TestApplyConfigDefersUntilSessionsExpire(t *testing.T) {
	// An atomic clock: the bots read the time from their own goroutines, so the
	// test must move it without racing them.
	var clock atomic.Int64
	clock.Store(t0.UnixNano())
	runner, channels, _ := buildFleetWithClock(t, func() time.Time {
		return time.Unix(0, clock.Load()).UTC()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["dev/alpha"].incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "active",
		Content: botflota.TextContent{Body: "hi"},
	}
	receive(t, channels["dev/alpha"].sent)

	candidate := twoScopeCandidate(t)
	scope := candidate.Scopes["dev"]
	graph := scope.Graphs["menu"]
	graph.Nodes["menu"] = spec.Node{Edges: []spec.Edge{{
		Intent: "continue", Goto: "next",
	}}}
	graph.Nodes["next"] = spec.Node{}
	scope.Graphs["menu"] = graph
	candidate.Scopes["dev"] = scope

	applied, pending, err := runner.ApplyConfig(ctx, candidate, []string{"dev"}, nil)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if len(applied) != 0 || len(pending["dev"]) == 0 {
		t.Fatalf("ApplyConfig = %v, %v; want dev pending with a live session",
			applied, pending)
	}

	// The same candidate is offered again while the session is still live: the
	// watcher retries, and it must stay pending rather than force the change.
	if _, pending, err := runner.ApplyConfig(ctx, candidate, []string{"dev"}, nil); err != nil {
		t.Fatalf("ApplyConfig retry: %v", err)
	} else if len(pending["dev"]) == 0 {
		t.Fatalf("retry applied a change whose session is still live")
	}

	// The session ages out; the next attempt at the same candidate applies it.
	clock.Store(t0.Add(2 * time.Minute).UnixNano())
	applied, pending, err = runner.ApplyConfig(ctx, candidate, []string{"dev"}, nil)
	if err != nil {
		t.Fatalf("ApplyConfig after expiry: %v", err)
	}
	if !slices.Equal(applied, []string{"dev"}) || len(pending) != 0 {
		t.Fatalf("ApplyConfig after expiry = %v, %v; want dev applied", applied, pending)
	}
	runner.mu.Lock()
	gen := runner.scopeGen["dev"]
	runner.mu.Unlock()
	if gen != 1 {
		t.Fatalf("scope generation = %d, want the deferred scope advanced once", gen)
	}
}
