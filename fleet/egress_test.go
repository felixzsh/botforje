package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/egress"
	"github.com/felixzsh/botflota/spec"
)

// TestFleetGivesTheEngineTheManagedHTTPClient proves the fleet hands the
// instance's egress policy to every engine it builds: a request step to a
// destination the policy refuses fails instead of dialing (ADR 0048).
func TestFleetGivesTheEngineTheManagedHTTPClient(t *testing.T) {
	cfg := &config.Config{Scopes: map[string]spec.Scope{
		"dev": {
			Actions: map[string]spec.Action{
				"reset": {Steps: []spec.Step{{Request: &spec.Request{
					Name: "reset", URL: "http://127.0.0.1:1/admin/reset",
				}}}},
			},
			Graphs: map[string]spec.Graph{
				"menu": {Root: "start", Nodes: map[string]spec.Node{
					"start": {OnEnter: []string{"reset"}},
				}},
			},
			Bots: map[string]spec.Bot{
				"alpha": {
					Graph:   "menu",
					Channel: spec.Channel{Provider: "whatsapp", Mode: "native"},
				},
			},
		},
	}}
	client, err := egress.Client(egress.PublicOnly)
	if err != nil {
		t.Fatalf("egress client: %v", err)
	}
	store := newStore()
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: store, Outbox: store,
		HTTPClient:     client,
		DefaultTimeout: time.Minute,
		Now:            func() time.Time { return t0 },
		Log:            botflota.DiscardLogger(),
		Catalog: fakeCatalog(func(string, string, any) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bot, err := runner.buildBot("dev", "alpha")
	if err != nil {
		t.Fatalf("buildBot: %v", err)
	}
	_, _, err = bot.engine.Handle(context.Background(), botflota.IncomingMessage{
		Sender: waKey("dev"), Target: waTarget("dev"),
		Content: botflota.TextContent{Body: "hi"},
	})
	if !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("Handle error = %v, want %v", err, egress.ErrBlocked)
	}
}
