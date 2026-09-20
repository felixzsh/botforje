package engine_test

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/engine"
	"github.com/felixzsh/botflota/spec"
)

var t0 = time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)

// memStore is an in-memory SenderStore, ChatStore, and SessionStore for engine
// tests.
type memStore struct {
	senders    map[string]*botflota.Sender
	chats      map[string]*botflota.Chat
	sessions   map[int64]*botflota.Session
	messages   []*botflota.Message
	nextSender int64
	nextChat   int64
	nextSess   int64
	nextMsg    int64
}

func newMemStore() *memStore {
	return &memStore{
		senders:  map[string]*botflota.Sender{},
		chats:    map[string]*botflota.Chat{},
		sessions: map[int64]*botflota.Session{},
	}
}

func senderKey(scope, phone string) string { return scope + "\x00" + phone }

func storeKey(scope, bot string, senderID int64) string {
	return scope + "\x00" + bot + "\x00" + strconv.FormatInt(senderID, 10)
}

func (s *memStore) Upsert(
	_ context.Context, scope, phone, name string, at time.Time,
) (*botflota.Sender, bool, error) {
	if sender, ok := s.senders[senderKey(scope, phone)]; ok {
		if name != "" && name != sender.DisplayName {
			sender.DisplayName = name
			sender.NameUpdatedAt = at
		}
		sender.LastSeen = at
		return sender, false, nil
	}
	s.nextSender++
	sender := &botflota.Sender{
		ID: s.nextSender, Scope: scope, Phone: phone, DisplayName: name,
		FirstSeen: at, LastSeen: at,
	}
	if name != "" {
		sender.NameUpdatedAt = at
	}
	s.senders[senderKey(scope, phone)] = sender
	return sender, true, nil
}

func (s *memStore) Get(
	_ context.Context, scope, phone string,
) (*botflota.Sender, error) {
	sender, ok := s.senders[senderKey(scope, phone)]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return sender, nil
}

func (s *memStore) Ensure(
	_ context.Context, scope, bot string, senderID int64, at time.Time,
) (*botflota.Chat, error) {
	key := storeKey(scope, bot, senderID)
	if chat, ok := s.chats[key]; ok {
		return chat, nil
	}
	s.nextChat++
	chat := &botflota.Chat{
		ID: s.nextChat, Scope: scope, Bot: bot, SenderID: senderID,
		StartedAt: at, UpdatedAt: at,
	}
	s.chats[key] = chat
	return chat, nil
}

func (s *memStore) ForSender(
	_ context.Context, scope, bot string, senderID int64,
) (*botflota.Chat, error) {
	chat, ok := s.chats[storeKey(scope, bot, senderID)]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return chat, nil
}

func (s *memStore) Recent(
	_ context.Context, scope, bot string, limit int,
) ([]botflota.ChatSummary, error) {
	return nil, nil
}

func (s *memStore) RecordMessage(_ context.Context, id int64, at time.Time) error {
	for _, chat := range s.chats {
		if chat.ID == id {
			chat.UpdatedAt = at
			chat.MessageCount++
			return nil
		}
	}
	return botflota.ErrNotFound
}

func (s *memStore) SaveGuards(
	_ context.Context, chatID int64, guards map[string]botflota.GuardState,
) error {
	for _, chat := range s.chats {
		if chat.ID == chatID {
			chat.Guards = guards
			return nil
		}
	}
	return botflota.ErrNotFound
}

func (s *memStore) Latest(_ context.Context, chatID int64) (*botflota.Session, error) {
	session, ok := s.sessions[chatID]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return session, nil
}

func (s *memStore) Save(_ context.Context, session *botflota.Session) error {
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

func (s *memStore) CountActive(
	_ context.Context, scope, bot string, at time.Time,
) (int, error) {
	count := 0
	for _, chat := range s.chats {
		if chat.Scope != scope || chat.Bot != bot {
			continue
		}
		if session, ok := s.sessions[chat.ID]; ok && !session.Expired(at) {
			count++
		}
	}
	return count, nil
}

func (s *memStore) Append(_ context.Context, message *botflota.Message) error {
	s.nextMsg++
	message.ID = s.nextMsg
	s.messages = append(s.messages, message)
	return nil
}

func (s *memStore) SetProviderID(_ context.Context, id int64, providerID string) error {
	for _, message := range s.messages {
		if message.ID == id {
			message.ProviderMessageID = providerID
			return nil
		}
	}
	return botflota.ErrNotFound
}

func (s *memStore) History(
	_ context.Context, chatID, afterID int64, limit int,
) ([]botflota.Message, error) {
	var out []botflota.Message
	for _, message := range s.messages {
		if message.ChatID == chatID && message.ID > afterID {
			out = append(out, *message)
		}
	}
	if limit > 0 && len(out) > limit {
		if afterID == 0 {
			out = out[len(out)-limit:]
		} else {
			out = out[:limit]
		}
	}
	return out, nil
}

// sessionFor reads a chat's current session by its config identity.
func (s *memStore) sessionFor(scope, bot, sender string) (*botflota.Session, error) {
	profile, ok := s.senders[senderKey(scope, sender)]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	chat, ok := s.chats[storeKey(scope, bot, profile.ID)]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return s.Latest(context.Background(), chat.ID)
}

func loadScope(t *testing.T) spec.Scope {
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

func newTestEngine(t *testing.T, opts engine.Options) *engine.Engine {
	t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return t0 }
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewSource(1))
	}
	return engine.New(opts)
}

func outbound(t *testing.T, eng *engine.Engine, sender, text string) []string {
	t.Helper()
	messages, err := eng.Handle(context.Background(), botflota.IncomingMessage{
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

func fixtureEngine(t *testing.T, store *memStore) *engine.Engine {
	t.Helper()
	scope := loadScope(t)
	return newTestEngine(t, engine.Options{
		Bot:            "support-bot",
		Scope:          "support",
		Graph:          scope.Graphs["faq"],
		Actions:        scope.Actions,
		Store:          store,
		Chats:          store,
		Senders:        store,
		Messages:       store,
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
	eng := engine.New(engine.Options{
		Bot: "support-bot", Scope: "support",
		Graph: scope.Graphs["faq"], Actions: scope.Actions,
		Store: store, Chats: store, Senders: store, Messages: store,
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
		Store: store, Chats: store, Senders: store, Messages: store, HTTPClient: server.Client(),
	})
	got := outbound(t, eng, "s", "hi")
	if want := []string{"got 7 status 200"}; !slices.Equal(got, want) {
		t.Fatalf("request result = %q, want %q", got, want)
	}
}

func TestCooldownGuard(t *testing.T) {
	now := t0
	actions := map[string]spec.Action{
		"warn": {
			Guards: &spec.Guards{Cooldown: &spec.Cooldown{
				Duration: 60,
				OnBlocked: []spec.Step{{Message: &spec.Message{
					Body: spec.Text{"already warned"},
				}}},
			}},
			Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"warning"}}}},
		},
	}
	graph := spec.Graph{
		Root: "menu",
		Nodes: map[string]spec.Node{
			"menu": {OnEnter: []string{"warn"}, Edges: []spec.Edge{{Goto: "menu"}}},
		},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Senders: store, Messages: store,
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

func TestRateLimitGuard(t *testing.T) {
	now := t0
	actions := map[string]spec.Action{
		"warn": {
			Guards: &spec.Guards{RateLimit: &spec.RateLimit{
				Limit:  2,
				Window: 60,
				OnBlocked: []spec.Step{{Message: &spec.Message{
					Body: spec.Text{"slow down"},
				}}},
			}},
			Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"warning"}}}},
		},
	}
	graph := spec.Graph{
		Root: "menu",
		Nodes: map[string]spec.Node{
			"menu": {OnEnter: []string{"warn"}, Edges: []spec.Edge{{Goto: "menu"}}},
		},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Senders: store, Messages: store,
		Now: func() time.Time { return now },
	})

	for i := 0; i < 2; i++ {
		if got := outbound(t, eng, "s", "a"); !slices.Equal(got, []string{"warning"}) {
			t.Fatalf("run %d = %q, want warning", i, got)
		}
	}
	if got := outbound(t, eng, "s", "b"); !slices.Equal(got, []string{"slow down"}) {
		t.Fatalf("blocked run = %q, want slow down", got)
	}
	now = now.Add(61 * time.Second)
	if got := outbound(t, eng, "s", "c"); !slices.Equal(got, []string{"warning"}) {
		t.Fatalf("after window = %q, want warning", got)
	}
}

func TestGuardStateOutlivesSessionExpiry(t *testing.T) {
	now := t0
	actions := map[string]spec.Action{
		"warn": {
			Guards: &spec.Guards{RateLimit: &spec.RateLimit{
				Limit:  1,
				Window: 3600,
				OnBlocked: []spec.Step{{Message: &spec.Message{
					Body: spec.Text{"slow down"},
				}}},
			}},
			Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"warning"}}}},
		},
	}
	graph := spec.Graph{
		Root: "menu",
		Nodes: map[string]spec.Node{
			"menu": {OnEnter: []string{"warn"}, Edges: []spec.Edge{{Goto: "menu"}}},
		},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Senders: store, Messages: store,
		DefaultTimeout: 300 * time.Second,
		Now:            func() time.Time { return now },
	})

	if got := outbound(t, eng, "s", "a"); !slices.Equal(got, []string{"warning"}) {
		t.Fatalf("first run = %q, want warning", got)
	}
	// Past the session timeout but inside the rate limit window: the guard
	// state lives on the chat, so it still blocks.
	now = now.Add(301 * time.Second)
	if got := outbound(t, eng, "s", "b"); !slices.Equal(got, []string{"slow down"}) {
		t.Fatalf("after session expiry = %q, want slow down", got)
	}
	// Another sender has its own chat and is not blocked.
	if got := outbound(t, eng, "t", "c"); !slices.Equal(got, []string{"warning"}) {
		t.Fatalf("other sender = %q, want warning", got)
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
		Store: store, Chats: store, Senders: store, Messages: store,
	})
	_, err := eng.Handle(context.Background(), botflota.IncomingMessage{Sender: "s", Body: "hi"})
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
		Store: store, Chats: store, Senders: store, Messages: store,
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
			Body: spec.Text{"lead from {{sender.phone}}"},
		}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"notify"}}},
	}
	store := newMemStore()
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Senders: store, Messages: store,
	})
	messages, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "b", Sender: "5215500000000", Body: "hola", At: t0,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one", messages)
	}
	if messages[0].Recipient != "5215500099999" {
		t.Fatalf("recipient = %q, want the override", messages[0].Recipient)
	}
	if messages[0].Body != "lead from 5215500000000" {
		t.Fatalf("body = %q", messages[0].Body)
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
		Store: store, Chats: store, Senders: store, Messages: store,
	})
	messages, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "b", Sender: "5215500000000", Body: "hola", At: t0,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 1 || messages[0].Recipient != "5215500000000" {
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
		Store: store, Chats: store, Senders: store, Messages: store,
	})
	_, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "b", Sender: "52155", Body: "hola", At: t0,
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
		Store: store, Chats: store, Senders: store, Messages: store,
	})
	messages, err := eng.Handle(context.Background(),
		botflota.IncomingMessage{Sender: "s", Body: "hi"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 1 || messages[0].Kind != botflota.KindLocation {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Location == nil || messages[0].Location.Name != "Office" {
		t.Fatalf("location = %#v", messages[0].Location)
	}
}

func TestSenderProfilePersistedOnFirstMessage(t *testing.T) {
	store := newMemStore()
	eng := fixtureEngine(t, store)
	if _, err := eng.Handle(context.Background(), botflota.IncomingMessage{
		Bot: "support-bot", Sender: "52155", SenderName: "Ana", Body: "hi", At: t0,
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	profile, err := store.Get(context.Background(), "support", "52155")
	if err != nil {
		t.Fatalf("sender not persisted: %v", err)
	}
	if profile.DisplayName != "Ana" || !profile.FirstSeen.Equal(t0) {
		t.Fatalf("profile = %#v", profile)
	}
	if len(store.chats) != 1 {
		t.Fatalf("chats = %d, want 1", len(store.chats))
	}
	for _, chat := range store.chats {
		if chat.SenderID != profile.ID {
			t.Fatalf("chat sender_id = %d, want the profile %d", chat.SenderID, profile.ID)
		}
	}
}

func TestMessagesRecordedUnderChatAndSession(t *testing.T) {
	store := newMemStore()
	eng := fixtureEngine(t, store)
	sender := "5215500000000"
	outbound(t, eng, sender, "1")

	ctx := context.Background()
	profile, err := store.Get(ctx, "support", sender)
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	chat, err := store.Ensure(ctx, "support", "support-bot", profile.ID, t0)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	messages, err := store.History(ctx, chat.ID, 0, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("recorded %d messages, want 2", len(messages))
	}
	inbound, reply := messages[0], messages[1]
	if inbound.Direction != botflota.Inbound || inbound.Body != "1" {
		t.Fatalf("inbound = %+v", inbound)
	}
	if reply.Direction != botflota.Outbound || reply.Body == "" {
		t.Fatalf("outbound = %+v", reply)
	}
	if inbound.SessionID == 0 || inbound.SessionID != reply.SessionID {
		t.Fatalf("session ids = %d, %d", inbound.SessionID, reply.SessionID)
	}
}

func TestSenderNameFeedsTemplates(t *testing.T) {
	actions := map[string]spec.Action{
		"greet": {Steps: []spec.Step{{Message: &spec.Message{
			Body: spec.Text{"hi {{sender.name}}"},
		}}}},
	}
	graph := spec.Graph{
		Root:  "start",
		Nodes: map[string]spec.Node{"start": {OnEnter: []string{"greet"}}},
	}
	store := newMemStore()
	ctx := context.Background()
	// A profile learned earlier fills the name when the message has none.
	if _, _, err := store.Upsert(ctx, "s", "52155", "Ana", t0); err != nil {
		t.Fatalf("seed sender: %v", err)
	}
	eng := newTestEngine(t, engine.Options{
		Bot: "b", Scope: "s", Graph: graph, Actions: actions,
		Store: store, Chats: store, Senders: store, Messages: store,
	})
	messages, err := eng.Handle(ctx, botflota.IncomingMessage{
		Bot: "b", Sender: "52155", Body: "hola", At: t0,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "hi Ana" {
		t.Fatalf("messages = %#v, want the stored name", messages)
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
		Store: store, Chats: store, Senders: store, Messages: store,
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
	_, err := eng.Handle(context.Background(), botflota.IncomingMessage{Sender: "s", Body: "hi"})
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
