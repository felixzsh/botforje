package engine_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/engine"
	"github.com/felixzsh/botflota/spec"
)

func TestRequestStepCapturesResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 7, "ok": true}`))
	}))
	defer server.Close()

	actions := map[string]spec.Action{
		"call": {Steps: []spec.Step{{Request: &spec.Request{
			Name: "get", URL: server.URL + "/data", Method: http.MethodGet,
		}}}},
		"report": {Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{
			"got {{node.get.resp.body.id}} status {{node.get.status}}",
		}}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"call", "report"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
		HTTPClient: server.Client(),
	})
	got := outbound(t, eng, "s", "hi")
	if want := []string{"got 7 status 200"}; !slices.Equal(got, want) {
		t.Fatalf("request result = %q, want %q", got, want)
	}
}

func TestIntentOnlyWithoutLLM(t *testing.T) {
	actions := map[string]spec.Action{
		"ask": {Steps: []spec.Step{{Message: &spec.Message{Intent: "ask something"}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"ask"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
	})
	_, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		FromPhone: "s", Content: botflota.TextContent{Body: "hi"},
	})
	if !errors.Is(err, botflota.ErrNoLLM) {
		t.Fatalf("error = %v, want ErrNoLLM", err)
	}
}

func TestAnchoredMessageFallsBackToBody(t *testing.T) {
	actions := map[string]spec.Action{
		"confirm": {Steps: []spec.Step{{Message: &spec.Message{
			Body:   spec.Text{"Your order is confirmed."},
			Intent: "confirm the order",
		}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"confirm"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
	})
	got := outbound(t, eng, "s", "hi")
	if want := []string{"Your order is confirmed."}; !slices.Equal(got, want) {
		t.Fatalf("anchored message = %q, want %q", got, want)
	}
}

func TestMessageToOverridesRecipient(t *testing.T) {
	actions := map[string]spec.Action{
		"notify": {Steps: []spec.Step{{Message: &spec.Message{
			To:   "5215500099999",
			Body: spec.Text{"lead from {{contact.phone}}"},
		}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"notify"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
	})
	messages, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "b", FromPhone: "5215500000000", At: t0,
		Content: botflota.TextContent{Body: "hola"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one", messages)
	}
	if messages[0].RecipientPhone != "5215500099999" {
		t.Fatalf("recipient = %q, want the override", messages[0].RecipientPhone)
	}
	if got := botflota.ContentText(messages[0].Content); got != "lead from 5215500000000" {
		t.Fatalf("body = %q", got)
	}
}

func TestMessageRecipientDefaultsToSender(t *testing.T) {
	actions := map[string]spec.Action{
		"greet": {Steps: []spec.Step{{Message: &spec.Message{
			Body: spec.Text{"hi"},
		}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"greet"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
	})
	messages, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "b", FromPhone: "5215500000000", At: t0,
		Content: botflota.TextContent{Body: "hola"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 1 || messages[0].RecipientPhone != "5215500000000" {
		t.Fatalf("messages = %#v, want the sender as recipient", messages)
	}
}

func TestMessageToResolvingEmptyFails(t *testing.T) {
	actions := map[string]spec.Action{
		"notify": {Steps: []spec.Step{{Message: &spec.Message{
			To:   "{{graph.owner_phone}}",
			Body: spec.Text{"hi"},
		}}}},
	}
	graph := spec.Graph{
		Root:           "start",
		InitialContext: map[string]any{"owner_phone": ""},
		Nodes:          map[string]spec.Node{"start": {OnEnter: []string{"notify"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
	})
	_, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "b", FromPhone: "52155", At: t0,
		Content: botflota.TextContent{Body: "hola"},
	})
	if err == nil || !strings.Contains(err.Error(), "resolved empty") {
		t.Fatalf("error = %v, want a resolved-empty recipient error", err)
	}
}

func TestLocationStep(t *testing.T) {
	actions := map[string]spec.Action{
		"office": {Steps: []spec.Step{{Location: &spec.Location{
			Latitude: 19.43, Longitude: -99.13, Name: "Office",
		}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"office"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
	})
	messages, err := eng.Handle(context.Background(),
		botflota.IncomingMessage{FromPhone: "s", Content: botflota.TextContent{Body: "hi"}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 1 || messages[0].Content.MessageKind() != botflota.KindLocation {
		t.Fatalf("messages = %#v", messages)
	}
	location, ok := messages[0].Content.(botflota.LocationContent)
	if !ok || location.Name != "Office" {
		t.Fatalf("location = %#v", messages[0].Content)
	}
}

func TestMediaContactPollSteps(t *testing.T) {
	actions := map[string]spec.Action{
		"photo": {Steps: []spec.Step{{Media: &spec.Media{
			Kind: "image", URL: "https://cdn.example.com/a.png", Caption: "Look",
		}}}},
		"card": {Steps: []spec.Step{{Contact: &spec.Contact{
			Name: "Ana", Phone: "5215500000000",
		}}}},
		"vote": {Steps: []spec.Step{{Poll: &spec.Poll{
			Question: "Lunch?", Options: []string{"yes", "no"},
		}}}},
	}
	graph := spec.Graph{
		Root: "start",
		Nodes: map[string]spec.Node{
			"start": {OnEnter: []string{"photo", "card", "vote"}},
		},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
	})
	messages, err := eng.Handle(context.Background(),
		botflota.IncomingMessage{FromPhone: "s", Content: botflota.TextContent{Body: "hi"}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	image, ok := messages[0].Content.(botflota.MediaContent)
	if !ok || image.URL == "" || image.Caption != "Look" {
		t.Fatalf("image = %#v", messages[0].Content)
	}
	contact, ok := messages[1].Content.(botflota.ContactContent)
	if !ok || contact.Phone != "5215500000000" {
		t.Fatalf("contact = %#v", messages[1].Content)
	}
	poll, ok := messages[2].Content.(botflota.PollContent)
	if !ok || len(poll.Options) != 2 {
		t.Fatalf("poll = %#v", messages[2].Content)
	}
}

type flakyTransport struct {
	calls int
	fail  int
}

func (f *flakyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	if f.calls <= f.fail {
		return nil, errors.New("connection refused")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok": true}`)),
	}, nil
}

func retryEngine(t *testing.T, retry int, transport http.RoundTripper) *engine.Engine {
	t.Helper()
	actions := map[string]spec.Action{
		"call": {Steps: []spec.Step{{Request: &spec.Request{
			Name: "ping", URL: "http://example.test", Retry: retry,
		}}}},
		"report": {Steps: []spec.Step{{Message: &spec.Message{
			Body: spec.Text{"ok={{node.ping.resp.body.ok}}"},
		}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"call", "report"}}},
	}
	store := newMemStore()
	return newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Contacts: store, Messages: store, Turn: store,
		HTTPClient: &http.Client{Transport: transport},
	})
}

func TestRequestRetriesThenSucceeds(t *testing.T) {
	transport := &flakyTransport{fail: 1}
	got := outbound(t, retryEngine(t, 1, transport), "s", "hi")
	if want := []string{"ok=true"}; !slices.Equal(got, want) {
		t.Fatalf("retry result = %q, want %q", got, want)
	}
	if transport.calls != 2 {
		t.Fatalf("attempts = %d, want 2", transport.calls)
	}
}

func TestRequestFailureStopsTheRun(t *testing.T) {
	eng := retryEngine(t, 1, &flakyTransport{fail: 5})
	_, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		FromPhone: "s", Content: botflota.TextContent{Body: "hi"},
	})
	if err == nil {
		t.Fatal("want an error when every attempt fails")
	}
}
