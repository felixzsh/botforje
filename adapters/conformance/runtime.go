package conformance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
	"github.com/felixzsh/botflota/spec"
)

const (
	scopeName  = "conf"
	botName    = "bot"
	graphName  = "main"
	actionName = "reply"
)

// fixture is one running instance over the real SQLite stores, fleet, engine,
// and outbox with one bot on the subject's definition.
type fixture struct {
	t       *testing.T
	subject Subject
	db      *sqlite.DB
	path    string
	cfg     *config.Config
	fleet   *fleet.Fleet
	cancel  context.CancelFunc
	done    chan error
}

func newFixture(t *testing.T, s Subject, mutate func(*config.Config)) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "botflota.db")
	return newFixtureAt(t, s, path, mutate)
}

func newFixtureAt(
	t *testing.T, s Subject, path string, mutate func(*config.Config),
) *fixture {
	t.Helper()
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("conformance: open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := fixtureConfig(s)
	if mutate != nil {
		mutate(cfg)
	}
	cfg.DataDir = filepath.Dir(path)

	binding := s.Binding(scopeName, botName)
	if err := db.Bindings().Commit(context.Background(), &binding); err != nil {
		t.Fatalf("conformance: commit binding: %v", err)
	}
	live, err := fleet.New(cfg, fleet.Options{
		Store: db.Sessions(), Chats: db.Chats(), Peers: db.Peers(),
		Messages: db.Messages(), Turn: db.Turns(), Outbox: db.Outbox(),
		Bindings: db.Bindings(), Catalog: mustCatalog(t, s.Install(db.Bindings())),
		DefaultTimeout: time.Minute, Now: time.Now,
		Log: botflota.DiscardLogger(),
	})
	if err != nil {
		t.Fatalf("conformance: build fleet: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- live.Run(runCtx) }()
	f := &fixture{
		t: t, subject: s, db: db, path: path, cfg: cfg,
		fleet: live, cancel: cancel, done: done,
	}
	f.waitRunning()
	return f
}

// fixtureConfig is one scope, graph, and bot whose root action replies with
// text, so every subject exercises the same neutral behavior.
func fixtureConfig(s Subject) *config.Config {
	def := s.declaration()
	return &config.Config{
		DefaultTimeout: 60,
		Scopes: map[string]spec.Scope{scopeName: {
			Actions: map[string]spec.Action{actionName: {
				Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"hello"}}}},
			}},
			Graphs: map[string]spec.Graph{graphName: {
				Root: "root",
				Nodes: map[string]spec.Node{
					"root": {OnEnter: []string{actionName}},
				},
			}},
			Bots: map[string]spec.Bot{botName: {
				Graph: graphName,
				Channel: spec.Channel{
					Provider: def.Provider,
					Mode:     def.Modes[0],
					Options:  s.Options,
				},
			}},
		}},
	}
}

func (f *fixture) waitRunning() {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, status := range f.fleet.Status() {
			if status.Scope == scopeName && status.Bot == botName &&
				status.State != "stopped" {
				return
			}
		}
		if time.Now().After(deadline) {
			f.t.Fatal("conformance: the bot never joined the running fleet")
		}
		time.Sleep(time.Millisecond)
	}
}

// stop ends the fleet and waits for its full shutdown; the database stays open
// so a caller can restart over it or read its state.
func (f *fixture) stop() {
	f.t.Helper()
	f.cancel()
	select {
	case err := <-f.done:
		if err != nil {
			f.t.Fatalf("conformance: fleet run: %v", err)
		}
	case <-time.After(5 * time.Second):
		f.t.Fatal("conformance: the fleet did not stop")
	}
}

func (f *fixture) channel() Channel { return f.subject.Channel(scopeName, botName) }

func (f *fixture) inject(target botflota.ChatTarget, id, sender string) {
	f.channel().Inject(botflota.IncomingMessage{
		Bot: botName,
		Sender: botflota.IdentityKey{
			Provider: f.subject.declaration().Provider, Subject: sender,
		},
		Target:            target,
		Content:           botflota.TextContent{Body: "hello"},
		ProviderMessageID: id,
		At:                time.Now(),
	})
}

func runRuntime(t *testing.T, s Subject) {
	t.Helper()
	t.Run("durable-send", func(t *testing.T) { testDurableSend(t, s) })
	t.Run("conversation-reads", func(t *testing.T) { testConversationReads(t, s) })
	t.Run("filter", func(t *testing.T) { testFilter(t, s) })
	t.Run("hold", func(t *testing.T) { testHold(t, s) })
	t.Run("restart", func(t *testing.T) { testRestart(t, s) })
	t.Run("detach", func(t *testing.T) { testDetach(t, s) })
}

// testDurableSend proves one inbound turn traverses the real engine and outbox
// to the channel, and that the channel's provider id is stored under the
// adapter-declared message space (ADR 0026, ADR 0052).
func testDurableSend(t *testing.T, s Subject) {
	f := newFixture(t, s, nil)
	defer f.stop()
	target := supportedTarget(s)
	channel := f.channel()
	f.inject(target, "out-1", "peer-out")
	// The reply is durable only once its provider id is recorded, which happens
	// after the channel's Send returned, so the store is the real barrier.
	waitFor(t, "the durable send", func() bool {
		return storedProviderID(f, target) != ""
	})
	delivered := channel.Sent()[0]
	if got := botflota.ContentText(delivered.Content); got != "hello" {
		t.Fatalf("delivered body = %q, want hello", got)
	}
	if delivered.Target != target {
		t.Fatalf("delivered target = %+v, want %+v", delivered.Target, target)
	}

	providerID := storedProviderID(f, target)
	if providerID == "" {
		t.Fatal("the delivered reply stored no provider id")
	}
	space := messageSpace(s, f)
	ok, err := f.db.Messages().HasProviderID(
		context.Background(), scopeName, botName, space, target, providerID)
	if err != nil {
		t.Fatalf("HasProviderID: %v", err)
	}
	if !ok {
		t.Fatalf("provider id %q is not stored under space %q", providerID, space)
	}
	if space != "" {
		other, err := f.db.Messages().HasProviderID(
			context.Background(), scopeName, botName, space+"-other", target, providerID)
		if err != nil {
			t.Fatalf("HasProviderID other space: %v", err)
		}
		if other {
			t.Fatal("a provider id leaked into another message space")
		}
	}
}

// storedProviderID is the provider id the store holds for the newest outbound
// reply of one conversation, or empty while none is recorded yet.
func storedProviderID(f *fixture, target botflota.ChatTarget) string {
	chat, err := f.db.Chats().ForTarget(context.Background(), scopeName, botName, target)
	if err != nil {
		return ""
	}
	history, err := f.db.Messages().History(context.Background(), chat.ID, 0, 10)
	if err != nil {
		return ""
	}
	providerID := ""
	for _, message := range history {
		if message.Direction == botflota.Outbound && message.ProviderMessageID != "" {
			providerID = message.ProviderMessageID
		}
	}
	return providerID
}

// messageSpace resolves the subject's declared space for the committed binding.
func messageSpace(s Subject, f *fixture) string {
	def := s.declaration()
	if def.MessageSpace == nil {
		return ""
	}
	binding, err := f.db.Bindings().Get(context.Background(), scopeName, botName)
	if err != nil {
		f.t.Fatalf("read binding: %v", err)
	}
	return def.MessageSpace(binding)
}

// testConversationReads proves the stored conversation is readable through the
// real API reads after a live turn.
func testConversationReads(t *testing.T, s Subject) {
	f := newFixture(t, s, nil)
	defer f.stop()
	target := supportedTarget(s)
	channel := f.channel()
	f.inject(target, "read-1", "peer-read")
	waitFor(t, "the durable send", func() bool { return len(channel.Sent()) >= 1 })
	api := newAPIFixture(t, s, f)

	recorder := api.get(t, "/api/v1/scopes/conf/bots/bot/chats")
	if recorder.Code != 200 {
		t.Fatalf("chats status = %d, want 200: %s",
			recorder.Code, recorder.Body.String())
	}
	var chats struct {
		Chats []struct {
			ID     int64  `json:"id"`
			Thread string `json:"-"` // filled below from the target
		} `json:"chats"`
	}
	decodeJSON(t, recorder.Body.Bytes(), &chats)
	if len(chats.Chats) != 1 {
		t.Fatalf("chats = %d, want the one conversation", len(chats.Chats))
	}
	chatID := chats.Chats[0].ID

	messages := api.get(t, fmt.Sprintf(
		"/api/v1/scopes/conf/messages?scope=conf&bot=bot&chat_id=%d", chatID))
	if messages.Code != 200 {
		t.Fatalf("messages status = %d, want 200: %s",
			messages.Code, messages.Body.String())
	}
	var body struct {
		Messages []struct {
			Direction string `json:"direction"`
			Body      string `json:"body"`
		} `json:"messages"`
	}
	decodeJSON(t, messages.Body.Bytes(), &body)
	if len(body.Messages) != 2 {
		t.Fatalf("messages = %d, want the inbound and its reply", len(body.Messages))
	}
	if body.Messages[0].Direction != string(botflota.Inbound) ||
		body.Messages[1].Direction != string(botflota.Outbound) {
		t.Fatalf("directions = %s, %s", body.Messages[0].Direction, body.Messages[1].Direction)
	}
}

// testFilter proves an ignored sender is a deliberate discard: it stores
// nothing and queues nothing, while its allowed peer still gets its reply
// (ADR 0051).
func testFilter(t *testing.T, s Subject) {
	f := newFixture(t, s, func(cfg *config.Config) {
		scope := cfg.Scopes[scopeName]
		bot := scope.Bots[botName]
		bot.Settings = &spec.BotSettings{IgnoredPeers: []string{"peer-ignored"}}
		scope.Bots[botName] = bot
		cfg.Scopes[scopeName] = scope
	})
	defer f.stop()
	channel := f.channel()
	ignored := supportedTarget(s)
	ignored.Thread = ignored.Thread + "-ignored"
	f.inject(ignored, "filter-ignored", "peer-ignored")
	allowed := supportedTarget(s)
	f.inject(allowed, "filter-allowed", "peer-allowed")
	waitFor(t, "the allowed reply", func() bool { return len(channel.Sent()) >= 1 })
	if len(channel.Sent()) != 1 {
		t.Fatalf("deliveries = %d, want only the allowed reply", len(channel.Sent()))
	}
	if _, err := f.db.Chats().ForTarget(
		context.Background(), scopeName, botName, ignored,
	); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("a filtered sender was stored: %v", err)
	}
}

// testHold proves a held chat stores the inbound without replying, and the
// reply resumes once the hold is released (ADR 0043).
func testHold(t *testing.T, s Subject) {
	f := newFixture(t, s, nil)
	defer f.stop()
	target := supportedTarget(s)
	channel := f.channel()
	if err := f.fleet.SetChatHuman(
		context.Background(), scopeName, botName, target, true,
	); err != nil {
		t.Fatalf("hold chat: %v", err)
	}
	f.inject(target, "hold-1", "peer-hold")
	waitFor(t, "the held inbound to be stored", func() bool {
		chat, err := f.db.Chats().ForTarget(
			context.Background(), scopeName, botName, target)
		if err != nil {
			return false
		}
		history, err := f.db.Messages().History(context.Background(), chat.ID, 0, 10)
		return err == nil && len(history) > 0
	})
	if len(channel.Sent()) != 0 {
		t.Fatal("a held chat answered")
	}
	if err := f.fleet.SetChatHuman(
		context.Background(), scopeName, botName, target, false,
	); err != nil {
		t.Fatalf("release chat: %v", err)
	}
	f.inject(target, "hold-2", "peer-hold")
	waitFor(t, "the reply after release", func() bool {
		return len(channel.Sent()) >= 1
	})
}

// testRestart proves a restart over the same database keeps the committed
// binding and the stored conversation, and can deliver again.
func testRestart(t *testing.T, s Subject) {
	path := filepath.Join(t.TempDir(), "botflota.db")
	first := newFixtureAt(t, s, path, nil)
	target := supportedTarget(s)
	first.inject(target, "restart-1", "peer-restart")
	waitFor(t, "the first run's reply", func() bool {
		return len(first.channel().Sent()) >= 1
	})
	generation := bindingGeneration(t, first)
	first.stop()
	if err := first.db.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	second := newFixtureAt(t, s, path, nil)
	defer second.stop()
	if got := bindingGeneration(t, second); got != generation {
		t.Fatalf("generation after restart = %d, want %d", got, generation)
	}
	// A new conversation, because the first one's root action ran once for
	// its new session and the engine does not replay a root entry.
	secondTarget := target
	secondTarget.Thread = target.Thread + "-after-restart"
	second.inject(secondTarget, "restart-2", "peer-restart")
	waitFor(t, "the restarted run's reply", func() bool {
		return len(second.channel().Sent()) >= 1
	})
	chat, err := second.db.Chats().ForTarget(
		context.Background(), scopeName, botName, target)
	if err != nil {
		t.Fatalf("the first conversation was lost across restart: %v", err)
	}
	history, err := second.db.Messages().History(context.Background(), chat.ID, 0, 20)
	if err != nil {
		t.Fatalf("read history after restart: %v", err)
	}
	if len(history) < 2 {
		t.Fatalf("history after restart = %d rows, want the first conversation kept",
			len(history))
	}
}

func bindingGeneration(t *testing.T, f *fixture) int64 {
	t.Helper()
	binding, err := f.db.Bindings().Get(context.Background(), scopeName, botName)
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	return binding.Generation
}

// testDetach proves the definition's neutral lifecycle forgets the binding and
// leaves the stored conversation alone (ADR 0053).
func testDetach(t *testing.T, s Subject) {
	if s.declaration().Setup == nil {
		t.Skip("the definition offers no account setup")
	}
	f := newFixture(t, s, nil)
	target := supportedTarget(s)
	f.inject(target, "detach-1", "peer-detach")
	waitFor(t, "the reply before detach", func() bool {
		return len(f.channel().Sent()) >= 1
	})
	f.stop()

	lifecycle, runner, err := f.fleet.SetupFor(scopeName, botName)
	if err != nil {
		t.Fatalf("SetupFor: %v", err)
	}
	methods, err := runner.SetupMethods(context.Background(), scopeName, botName)
	if err != nil {
		t.Fatalf("SetupMethods: %v", err)
	}
	if len(methods) == 0 {
		t.Fatal("the setup runner declares no method")
	}
	status, err := lifecycle.InspectBinding(context.Background(), scopeName, botName)
	if err != nil {
		t.Fatalf("InspectBinding: %v", err)
	}
	if status.Condition != botflota.ReadinessConfigured {
		t.Fatalf("readiness = %s, want configured", status.Condition)
	}
	if err := lifecycle.DetachBinding(context.Background(), scopeName, botName); err != nil {
		t.Fatalf("DetachBinding: %v", err)
	}
	if _, err := f.db.Bindings().Get(
		context.Background(), scopeName, botName,
	); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("the binding survived detach: %v", err)
	}
	if _, err := f.db.Chats().ForTarget(
		context.Background(), scopeName, botName, target,
	); err != nil {
		t.Fatalf("detach destroyed the stored conversation: %v", err)
	}
}

// waitFor polls until ready or the deadline; it returns the value of the check
// only as a convenience for callers that ignore it.
func waitFor(t *testing.T, what string, ready func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ready() {
			return true
		}
		if time.Now().After(deadline) {
			t.Fatalf("conformance: timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
