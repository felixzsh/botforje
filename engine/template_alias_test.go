package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/engine"
	"github.com/felixzsh/botflota/spec"
)

// TestLegacyTemplateAliasResolvesForTheConsumingBot proves the compatibility
// translation happens at resolution, per bot: the definition that owns the alias
// renders the neutral path, and a bot whose definition declares none fails
// instead of rendering an empty value (ADR 0018/0020).
func TestLegacyTemplateAliasResolvesForTheConsumingBot(t *testing.T) {
	graph := spec.Graph{
		Root: "root",
		Nodes: map[string]spec.Node{
			"root": {OnEnter: []string{"greet"}},
		},
	}
	actions := map[string]spec.Action{"greet": {Steps: []spec.Step{
		{Message: &spec.Message{Body: spec.Text{"Phone: {{contact.phone}}"}}},
	}}}
	newEngine := func(aliases map[string]string) *engine.Engine {
		store := newMemStore()
		return newTestEngine(t, engine.Options{
			Bot: "support-bot", Scope: "support", Graph: graph, Actions: actions,
			Store: store, Chats: store, Peers: store, Messages: store, Turn: store,
			DefaultTimeout:  300 * time.Second,
			TemplateAliases: aliases,
		})
	}

	withAlias := newEngine(map[string]string{"contact.phone": "peer.subject"})
	if got := outbound(t, withAlias, "5215500000000", "hi"); len(got) != 1 ||
		got[0] != "Phone: 5215500000000" {
		t.Fatalf("outbound = %q, want the neutral peer subject", got)
	}

	withoutAlias := newEngine(nil)
	_, err := withoutAlias.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "support-bot", Sender: waKey("5215500000000"),
		Target: waTarget("5215500000000"), At: t0,
		Content: botflota.TextContent{Body: "hi"},
	})
	if err == nil {
		t.Fatal("a reference no alias declares must fail, not render empty")
	}
}
