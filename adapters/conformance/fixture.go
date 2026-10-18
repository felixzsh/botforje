package conformance

import (
	"context"
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
	return newFixtureBots(t, s, path, []contrastBot{{
		name:    botName,
		options: optionsFor(s, scopeName, botName),
		binding: s.Binding(scopeName, botName),
	}}, mutate)
}

func newFixtureBots(
	t *testing.T, s Subject, path string, bots []contrastBot,
	mutate func(*config.Config),
) *fixture {
	t.Helper()
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("conformance: open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cfg := fixtureConfigBots(s, bots)
	if mutate != nil {
		mutate(cfg)
	}
	cfg.DataDir = filepath.Dir(path)

	for _, bot := range bots {
		binding := bot.binding
		if err := db.Bindings().Commit(
			context.Background(), &binding,
		); err != nil {
			t.Fatalf("conformance: commit binding for %s: %v", bot.name, err)
		}
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
	f.waitRunning(names(bots))
	return f
}

// fixtureConfigBots is one scope and graph whose root action replies with
// text, so every subject exercises the same neutral behavior.
func fixtureConfigBots(s Subject, bots []contrastBot) *config.Config {
	def := s.declaration()
	configured := map[string]spec.Bot{}
	for _, bot := range bots {
		configured[bot.name] = spec.Bot{
			Graph: graphName,
			Channel: spec.Channel{
				Provider: def.Provider,
				Mode:     def.Modes[0],
				Options:  bot.options,
			},
		}
	}
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
			Bots: configured,
		}},
	}
}

func names(bots []contrastBot) []string {
	list := make([]string, 0, len(bots))
	for _, bot := range bots {
		list = append(list, bot.name)
	}
	return list
}

func (f *fixture) waitRunning(names []string) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if f.allRunning(names) {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatal("conformance: a bot never joined the running fleet")
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *fixture) allRunning(names []string) bool {
	running := map[string]bool{}
	for _, status := range f.fleet.Status() {
		if status.Scope == scopeName && status.State != "stopped" {
			running[status.Bot] = true
		}
	}
	for _, name := range names {
		if !running[name] {
			return false
		}
	}
	return true
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

func (f *fixture) channel() Channel { return f.channelFor(botName) }

func (f *fixture) channelFor(bot string) Channel {
	return f.subject.Channel(scopeName, bot)
}

func (f *fixture) inject(target botflota.ChatTarget, id, sender string) {
	f.injectFor(botName, target, id, sender)
}

func (f *fixture) injectFor(
	bot string, target botflota.ChatTarget, id, sender string,
) {
	f.deliverFor(bot, botflota.IncomingMessage{
		Bot: bot,
		Sender: botflota.IdentityKey{
			Provider: f.subject.declaration().Provider, Subject: sender,
		},
		Target:            target,
		Content:           botflota.TextContent{Body: "hello"},
		ProviderMessageID: id,
		At:                time.Now(),
	})
}

// deliverFor hands one inbound to a running bot, through the subject's own
// mapping when it declares one and directly through the channel otherwise.
func (f *fixture) deliverFor(bot string, msg botflota.IncomingMessage) {
	if f.subject.Deliver != nil {
		f.subject.Deliver(scopeName, bot, msg)
		return
	}
	f.channelFor(bot).Inject(msg)
}
