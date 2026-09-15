package engine

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botforje/internal/config"
	"github.com/felixzsh/botforje/internal/domain"
)

var t0 = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

// memStore is an in-memory ChatStore and SessionStore for engine tests.
type memStore struct {
	chats    map[string]*domain.Chat
	sessions map[int64]*domain.Session
	nextChat int64
	nextSess int64
}

func newMemStore() *memStore {
	return &memStore{
		chats:    map[string]*domain.Chat{},
		sessions: map[int64]*domain.Session{},
	}
}

func storeKey(scope, bot, sender string) string { return scope + "\x00" + bot + "\x00" + sender }

func (s *memStore) Ensure(
	_ context.Context, scope, bot, sender string, at time.Time,
) (*domain.Chat, error) {
	key := storeKey(scope, bot, sender)
	if chat, ok := s.chats[key]; ok {
		return chat, nil
	}
	s.nextChat++
	chat := &domain.Chat{
		ID: s.nextChat, Scope: scope, Bot: bot, Sender: sender,
		StartedAt: at, UpdatedAt: at,
	}
	s.chats[key] = chat
	return chat, nil
}

func (s *memStore) Touch(_ context.Context, id int64, at time.Time) error {
	for _, chat := range s.chats {
		if chat.ID == id {
			chat.UpdatedAt = at
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *memStore) Latest(_ context.Context, chatID int64) (*domain.Session, error) {
	session, ok := s.sessions[chatID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return session, nil
}

func (s *memStore) Save(_ context.Context, session *domain.Session) error {
	if session.ID == 0 {
		s.nextSess++
		session.ID = s.nextSess
	}
	s.sessions[session.ChatID] = session
	return nil
}

func (s *memStore) Delete(_ context.Context, chatID int64) error {
	delete(s.sessions, chatID)
	return nil
}

// sessionFor reads a chat's current session by its config identity.
func (s *memStore) sessionFor(scope, bot, sender string) (*domain.Session, error) {
	chat, ok := s.chats[storeKey(scope, bot, sender)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s.Latest(context.Background(), chat.ID)
}

func loadScope(t *testing.T) config.Scope {
	t.Helper()
	cfg, err := (config.Loader{}).Load("testdata/support.yml")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if errs := config.Validate(cfg); len(errs) != 0 {
		t.Fatalf("fixture is invalid: %v", errs)
	}
	return cfg.Scopes["support"]
}

func newTestEngine(t *testing.T, opts Options) *Engine {
	t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return t0 }
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewSource(1))
	}
	return New(opts)
}

func outbound(t *testing.T, eng *Engine, sender, text string) []string {
	t.Helper()
	messages, err := eng.Handle(context.Background(), domain.IncomingMessage{
		Bot: "support-bot", Sender: sender, Body: text, At: t0,
	})
	if err != nil {
		t.Fatalf("Handle(%q): %v", text, err)
	}
	bodies := make([]string, 0, len(messages))
	for _, message := range messages {
		bodies = append(bodies, message.Body)
	}
	return bodies
}

func fixtureEngine(t *testing.T, store *memStore) *Engine {
	t.Helper()
	scope := loadScope(t)
	return newTestEngine(t, Options{
		Bot:            "support-bot",
		Scope:          "support",
		Graph:          scope.Graphs["faq"],
		Actions:        scope.Actions,
		Store:          store,
		Chats:          store,
		DefaultTimeout: 300 * time.Second,
	})
}

func TestConversationScenario(t *testing.T) {
	store := newMemStore()
	eng := fixtureEngine(t, store)
	sender := "5215500000000"

	steps := []struct {
		text string
		want []string
	}{
		{"1", []string{"Support hours: Monday to Friday, 9:00-18:00"}},
		{"menu", []string{"leaving hours", "Main menu: 1 hours, 2 catalog, 0 exit"}},
		{"2", []string{"Catalog: https://example.com/catalog"}},
		{"0", []string{"Bye Ana!"}},
	}
	for _, step := range steps {
		if got := outbound(t, eng, sender, step.text); !slices.Equal(got, step.want) {
			t.Fatalf("Handle(%q) = %q, want %q", step.text, got, step.want)
		}
	}

	session, err := store.sessionFor("support", "support-bot", sender)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Node != "farewell" {
		t.Fatalf("node = %q, want farewell", session.Node)
	}
	want := []string{"hours", "menu", "catalog", "farewell"}
	if !slices.Equal(session.VisitedNodes, want) {
		t.Fatalf("visited nodes = %q, want %q", session.VisitedNodes, want)
	}
}

func TestFirstMessageRoutesBeforeEntry(t *testing.T) {
	eng := fixtureEngine(t, newMemStore())
	got := outbound(t, eng, "s1", "1")
	want := []string{"Support hours: Monday to Friday, 9:00-18:00"}
	if !slices.Equal(got, want) {
		t.Fatalf("routed first message = %q, want %q (root entry must be skipped)", got, want)
	}
}

func TestFirstMessageRunsRootEntryWhenUnmatched(t *testing.T) {
	eng := fixtureEngine(t, newMemStore())
	got := outbound(t, eng, "s2", "hello")
	want := []string{"Main menu: 1 hours, 2 catalog, 0 exit"}
	if !slices.Equal(got, want) {
		t.Fatalf("unmatched first message = %q, want %q", got, want)
	}
}

func TestDefaultEdgeAndFallback(t *testing.T) {
	eng := fixtureEngine(t, newMemStore())
	sender := "s3"
	outbound(t, eng, sender, "hello")

	// The menu's default edge catches unmatched input.
	got := outbound(t, eng, sender, "zzz")
	if want := []string{"Choose a number from the menu."}; !slices.Equal(got, want) {
		t.Fatalf("default edge = %q, want %q", got, want)
	}
	// Move to the terminal node, then rely on the graph fallback.
	outbound(t, eng, sender, "0")
	got = outbound(t, eng, sender, "zzz")
	if want := []string{"Choose a number from the menu."}; !slices.Equal(got, want) {
		t.Fatalf("graph fallback = %q, want %q", got, want)
	}
}

func TestSessionExpiresAndRestartsAtRoot(t *testing.T) {
	now := t0
	store := newMemStore()
	scope := loadScope(t)
	eng := New(Options{
		Bot: "support-bot", Scope: "support",
		Graph: scope.Graphs["faq"], Actions: scope.Actions, Store: store, Chats: store,
		DefaultTimeout: 300 * time.Second,
		Now:            func() time.Time { return now },
		Rand:           rand.New(rand.NewSource(1)),
	})
	sender := "s4"
	outbound(t, eng, sender, "1")
	now = t0.Add(301 * time.Second)
	got := outbound(t, eng, sender, "hello")
	if want := []string{"Main menu: 1 hours, 2 catalog, 0 exit"}; !slices.Equal(got, want) {
		t.Fatalf("after expiry = %q, want %q", got, want)
	}
	session, err := store.sessionFor("support", "support-bot", sender)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Node != "menu" || !session.CreatedAt.Equal(now) {
		t.Fatalf("expired session not restarted: %#v", session)
	}
}

func TestRequestStepCapturesResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 7, "ok": true}`))
	}))
	defer server.Close()

	actions := map[string]config.Action{
		"call": {Steps: []config.Step{{Request: &config.Request{
			Name: "get", URL: server.URL + "/data", Method: http.MethodGet,
		}}}},
		"report": {Steps: []config.Step{{Message: &config.Message{Body: config.Text{
			"got {{node.get.resp.body.id}} status {{node.get.status}}",
		}}}}},
	}
	graph := config.Graph{
		Root:  "start",
		Nodes: map[string]config.Node{"start": {OnEnter: []string{"call", "report"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, HTTPClient: server.Client(),
	})
	got := outbound(t, eng, "s", "hi")
	if want := []string{"got 7 status 200"}; !slices.Equal(got, want) {
		t.Fatalf("request result = %q, want %q", got, want)
	}
}

func TestCooldownGuard(t *testing.T) {
	now := t0
	actions := map[string]config.Action{
		"warn": {
			Guards: &config.Guards{Cooldown: &config.Cooldown{
				Duration: 60,
				OnBlocked: []config.Step{{Message: &config.Message{
					Body: config.Text{"already warned"},
				}}},
			}},
			Steps: []config.Step{{Message: &config.Message{Body: config.Text{"warning"}}}},
		},
	}
	graph := config.Graph{
		Root: "menu",
		Nodes: map[string]config.Node{
			"menu": {OnEnter: []string{"warn"}, Edges: []config.Edge{{Goto: "menu"}}},
		},
	}
	store := newMemStore()
	eng := newTestEngine(t, Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions, Store: store, Chats: store,
		Now: func() time.Time { return now },
	})

	if got := outbound(t, eng, "s", "a"); !slices.Equal(got, []string{"warning"}) {
		t.Fatalf("first run = %q, want warning", got)
	}
	if got := outbound(t, eng, "s", "b"); !slices.Equal(got, []string{"already warned"}) {
		t.Fatalf("blocked run = %q, want already warned", got)
	}
	now = now.Add(2 * time.Minute)
	if got := outbound(t, eng, "s", "c"); !slices.Equal(got, []string{"warning"}) {
		t.Fatalf("after cooldown = %q, want warning", got)
	}
}

func TestIntentOnlyWithoutLLM(t *testing.T) {
	actions := map[string]config.Action{
		"ask": {Steps: []config.Step{{Message: &config.Message{Intent: "ask something"}}}},
	}
	graph := config.Graph{
		Root:  "start",
		Nodes: map[string]config.Node{"start": {OnEnter: []string{"ask"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions, Store: store, Chats: store,
	})
	_, err := eng.Handle(context.Background(), domain.IncomingMessage{Sender: "s", Body: "hi"})
	if !errors.Is(err, domain.ErrNoLLM) {
		t.Fatalf("error = %v, want ErrNoLLM", err)
	}
}

func TestAnchoredMessageFallsBackToBody(t *testing.T) {
	actions := map[string]config.Action{
		"confirm": {Steps: []config.Step{{Message: &config.Message{
			Body:   config.Text{"Your order is confirmed."},
			Intent: "confirm the order",
		}}}},
	}
	graph := config.Graph{
		Root:  "start",
		Nodes: map[string]config.Node{"start": {OnEnter: []string{"confirm"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions, Store: store, Chats: store,
	})
	got := outbound(t, eng, "s", "hi")
	if want := []string{"Your order is confirmed."}; !slices.Equal(got, want) {
		t.Fatalf("anchored message = %q, want %q", got, want)
	}
}

func TestLocationStep(t *testing.T) {
	actions := map[string]config.Action{
		"office": {Steps: []config.Step{{Location: &config.Location{
			Latitude: 19.43, Longitude: -99.13, Name: "Office",
		}}}},
	}
	graph := config.Graph{
		Root:  "start",
		Nodes: map[string]config.Node{"start": {OnEnter: []string{"office"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions, Store: store, Chats: store,
	})
	messages, err := eng.Handle(context.Background(), domain.IncomingMessage{Sender: "s", Body: "hi"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 1 || messages[0].Kind != domain.KindLocation {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Location == nil || messages[0].Location.Name != "Office" {
		t.Fatalf("location = %#v", messages[0].Location)
	}
}

// flakyTransport fails the first fail calls, then answers JSON.
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

func retryEngine(t *testing.T, retry int, transport http.RoundTripper) *Engine {
	t.Helper()
	actions := map[string]config.Action{
		"call": {Steps: []config.Step{{Request: &config.Request{
			Name: "ping", URL: "http://example.test", Retry: retry,
		}}}},
		"report": {Steps: []config.Step{{Message: &config.Message{
			Body: config.Text{"ok={{node.ping.resp.body.ok}}"},
		}}}},
	}
	graph := config.Graph{
		Root:  "start",
		Nodes: map[string]config.Node{"start": {OnEnter: []string{"call", "report"}}},
	}
	store := newMemStore()
	return newTestEngine(t, Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, HTTPClient: &http.Client{Transport: transport},
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
	_, err := eng.Handle(context.Background(), domain.IncomingMessage{Sender: "s", Body: "hi"})
	if err == nil {
		t.Fatal("want an error when every attempt fails")
	}
}

func TestSameInputDifferentHistory(t *testing.T) {
	eng := fixtureEngine(t, newMemStore())

	fresh := outbound(t, eng, "hist-fresh", "menu")
	if want := []string{"Main menu: 1 hours, 2 catalog, 0 exit"}; !slices.Equal(fresh, want) {
		t.Fatalf("fresh session = %q, want %q", fresh, want)
	}

	visited := "hist-visited"
	outbound(t, eng, visited, "1")
	got := outbound(t, eng, visited, "menu")
	want := []string{"leaving hours", "Main menu: 1 hours, 2 catalog, 0 exit"}
	if !slices.Equal(got, want) {
		t.Fatalf("after visiting hours = %q, want %q (ADR 0010)", got, want)
	}
}

func TestMatchScore(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		trigger string
		want    float64
	}{
		{"exact", "hours", "hours", 1},
		{"exact with punctuation", "1.", "1", 1},
		{"contiguous words", "what are your hours", "hours", 0.9},
		{"all words any order", "menu please now", "menu now", 0.7},
		{"typo", "hourse", "hours", 0.83},
		{"unrelated", "zzz", "hours", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchScore(tc.text, tc.trigger)
			if diff := got - tc.want; diff > 0.01 || diff < -0.01 {
				t.Fatalf("matchScore(%q, %q) = %v, want %v", tc.text, tc.trigger, got, tc.want)
			}
		})
	}
}
