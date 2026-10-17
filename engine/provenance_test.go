package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/engine"
	"github.com/felixzsh/botflota/spec"
)

type scriptedLLM struct {
	body string
	err  error
}

func (s scriptedLLM) Classify(
	context.Context, botflota.ClassifyRequest,
) (botflota.ClassifyResult, error) {
	return botflota.ClassifyResult{}, nil
}

func (s scriptedLLM) Realize(context.Context, botflota.RealizeRequest) (string, error) {
	return s.body, s.err
}

func provenanceGraph() spec.Graph {
	return spec.Graph{
		Root: "menu",
		Nodes: map[string]spec.Node{
			"menu": {
				OnEnter: []string{"menu-msg"},
				Edges: []spec.Edge{
					{Intent: "exact", Triggers: []string{"hours"}, Goto: "done"},
					{Intent: "sequence", Triggers: []string{"opening hours"}, Goto: "done"},
					{Intent: "words", Triggers: []string{"catalog products"}, Goto: "done"},
					{Intent: "fuzzy", Triggers: []string{"book now"}, Goto: "done"},
					{Goto: "done"},
				},
			},
			"done": {OnEnter: []string{"ok"}},
		},
	}
}

var provenanceActions = map[string]spec.Action{
	"menu-msg": {Steps: []spec.Step{{Message: &spec.Message{
		Body: spec.Text{"menu"},
	}}}},
	"ok": {Steps: []spec.Step{{Message: &spec.Message{
		Body: spec.Text{"ok"},
	}}}},
}

func provenanceEngine(
	t *testing.T, store *memStore, graph spec.Graph, llm botflota.LLMProvider,
) *engine.Engine {
	t.Helper()
	return newTestEngine(t, engine.Options{
		Bot: "support-bot", Scope: "support", Graph: graph,
		Actions: provenanceActions,
		Store:   store, Chats: store, Peers: store, Messages: store, Turn: store,
		LLM: llm,
	})
}

func lastRecord(
	t *testing.T, store *memStore, direction botflota.Direction,
) botflota.Message {
	t.Helper()
	for i := len(store.messages) - 1; i >= 0; i-- {
		if store.messages[i].Direction == direction {
			return *store.messages[i]
		}
	}
	t.Fatalf("no stored %s message", direction)
	return botflota.Message{}
}

// TestRoutingMethodsAreRecorded covers one probe per matching layer: the
// inbound message and the reply that answered it carry the same method.
func TestRoutingMethodsAreRecorded(t *testing.T) {
	cases := []struct {
		name string
		text string
		want botflota.RoutingMethod
	}{
		{"exact", "hours", botflota.RoutingTriggerExact},
		{"sequence", "what are your opening hours", botflota.RoutingTriggerSequence},
		{"words", "products catalog", botflota.RoutingTriggerWords},
		{"fuzzy", "book nos", botflota.RoutingTriggerFuzzy},
		{"default", "zzz", botflota.RoutingDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			eng := provenanceEngine(t, store, provenanceGraph(), nil)
			outbound(t, eng, "s", "hi") // opens the session at the root
			outbound(t, eng, "s", tc.text)

			in := lastRecord(t, store, botflota.Inbound)
			if in.Routing != tc.want {
				t.Fatalf("inbound routing = %q, want %q", in.Routing, tc.want)
			}
			if in.Ingest != botflota.IngestLive || in.Origin != "" {
				t.Fatalf("inbound provenance = %+v", in)
			}
			reply := lastRecord(t, store, botflota.Outbound)
			if reply.Routing != tc.want || reply.Origin != botflota.OriginPreset {
				t.Fatalf("reply = %+v, want preset with routing %q", reply, tc.want)
			}
		})
	}
}

func TestRootRoutingOnSessionCreation(t *testing.T) {
	store := newMemStore()
	eng := provenanceEngine(t, store, provenanceGraph(), nil)
	outbound(t, eng, "s", "zzz")

	in := lastRecord(t, store, botflota.Inbound)
	if in.Routing != botflota.RoutingRoot {
		t.Fatalf("inbound routing = %q, want root", in.Routing)
	}
	reply := lastRecord(t, store, botflota.Outbound)
	if reply.Routing != botflota.RoutingRoot || reply.Origin != botflota.OriginPreset ||
		reply.Ingest != botflota.IngestLive {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestFallbackRouting(t *testing.T) {
	graph := spec.Graph{
		Root: "menu",
		Nodes: map[string]spec.Node{
			"menu": {OnEnter: []string{"menu-msg"}, Edges: []spec.Edge{
				{Intent: "exact", Triggers: []string{"hours"}, Goto: "done"},
			}},
			"done": {OnEnter: []string{"ok"}},
		},
		Fallback: "done",
	}
	store := newMemStore()
	eng := provenanceEngine(t, store, graph, nil)
	outbound(t, eng, "s", "hi")   // opens the session at the root
	outbound(t, eng, "s", "nope") // no trigger, no default edge

	if got := lastRecord(t, store, botflota.Inbound).Routing; got != botflota.RoutingFallback {
		t.Fatalf("inbound routing = %q, want fallback", got)
	}
}

// TestGeneratedReplyOrigin checks that only a body the model really produced
// is llm; a failed realization that keeps the anchor stays preset.
func TestGeneratedReplyOrigin(t *testing.T) {
	graph := spec.Graph{
		Root:  "menu",
		Nodes: map[string]spec.Node{"menu": {OnEnter: []string{"realize"}}},
	}
	actions := map[string]spec.Action{
		"realize": {Steps: []spec.Step{{Message: &spec.Message{
			Body: spec.Text{"anchor"}, Intent: "greet",
		}}}},
	}
	build := func(llm botflota.LLMProvider) (*memStore, *engine.Engine) {
		store := newMemStore()
		eng := newTestEngine(t, engine.Options{
			Bot: "support-bot", Scope: "support", Graph: graph, Actions: actions,
			Store: store, Chats: store, Peers: store, Messages: store, Turn: store,
			LLM: llm,
		})
		return store, eng
	}

	store, eng := build(scriptedLLM{body: "generated reply"})
	outbound(t, eng, "s", "hi")
	reply := lastRecord(t, store, botflota.Outbound)
	if reply.Origin != botflota.OriginLLM ||
		botflota.ContentText(reply.Content) != "generated reply" {
		t.Fatalf("reply = %+v, want an llm body", reply)
	}

	store, eng = build(scriptedLLM{err: errors.New("provider down")})
	outbound(t, eng, "s", "hi")
	reply = lastRecord(t, store, botflota.Outbound)
	if reply.Origin != botflota.OriginPreset ||
		botflota.ContentText(reply.Content) != "anchor" {
		t.Fatalf("reply = %+v, want the preset anchor", reply)
	}
}

func TestProviderMessageIDIsRecorded(t *testing.T) {
	store := newMemStore()
	eng := provenanceEngine(t, store, provenanceGraph(), nil)
	_, _, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "support-bot", Sender: waKey("s"), Target: waTarget("s"),
		ProviderMessageID: "ABC123", At: t0,
		Content: botflota.TextContent{Body: "hi"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	in := lastRecord(t, store, botflota.Inbound)
	if in.ProviderMessageID != "ABC123" {
		t.Fatalf("provider id = %q, want ABC123", in.ProviderMessageID)
	}
}

func TestOutboundRecordIDLinksTheStoredReply(t *testing.T) {
	store := newMemStore()
	eng := provenanceEngine(t, store, provenanceGraph(), nil)
	replies, _, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "support-bot", Sender: waKey("s"), Target: waTarget("s"),
		ProviderMessageID: "ABC123", At: t0,
		Content: botflota.TextContent{Body: "hi"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(replies) == 0 {
		t.Fatal("Handle returned no replies")
	}
	reply := lastRecord(t, store, botflota.Outbound)
	if replies[0].StoredMessageID == 0 || replies[0].StoredMessageID != reply.ID {
		t.Fatalf("StoredMessageID = %d, want the stored reply id %d",
			replies[0].StoredMessageID, reply.ID)
	}
}
