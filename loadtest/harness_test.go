package loadtest

// The harness assembles the real config, fleet, engine, SQLite stores, outbox,
// and channel catalog over the synthetic adapter. Only the adapter's in-memory
// provider is simulated; everything a bot's turn touches is production code.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/testchannel"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
	"github.com/felixzsh/botflota/spec"
)

const graphName = "main"

// layout describes one fleet: bots spread over scopes, each bot serving the
// same number of direct conversations.
type layout struct {
	Scopes int
	Bots   int
	Chats  int
	// Shared puts every bot in one adapter-declared message space, so a test
	// can prove two bots never share dedup or history through it.
	Shared bool
	// Pacing mirrors the production outbox settings; zero means immediate
	// delivery, which is what a capacity run without pacing measures.
	QueueDelayMS int
	JitterMS     int
	MaxPerMinute int
}

// harness is one running fleet over a temporary database.
type harness struct {
	t       *testing.T
	layout  layout
	path    string
	db      *sqlite.DB
	backend *testchannel.Backend
	fleet   *fleet.Fleet
	cancel  context.CancelFunc
	done    chan error

	mu       sync.Mutex
	errCount int
	errs     []error
}

// recordError keeps what the fleet reported instead of discarding it: a load
// run must be able to say why work did not complete.
func (h *harness) recordError(_ string, _ string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.errCount++
	if len(h.errs) < 32 {
		h.errs = append(h.errs, err)
	}
}

// errorCount is the number of failed turns the fleet reported, including those
// beyond the retained sample.
func (h *harness) errorCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.errCount
}

// totalSent is the number of replies every bot recorded.
func (h *harness) totalSent() int {
	total := 0
	for i := 0; i < h.layout.Bots; i++ {
		total += h.channel(i).SentCount()
	}
	return total
}

// errors returns the fleet-reported failures kept so far.
func (h *harness) errors() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]error(nil), h.errs...)
}

func newHarness(t *testing.T, l layout) *harness {
	t.Helper()
	return newHarnessAt(t, l, filepath.Join(t.TempDir(), "load.db"))
}

func newHarnessAt(t *testing.T, l layout, path string) *harness {
	t.Helper()
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	h := &harness{t: t, layout: l, path: path, db: db,
		backend: testchannel.NewBackend()}
	cfg := l.config(t)
	cfg.DataDir = filepath.Dir(path)
	for i := 0; i < l.Bots; i++ {
		binding := l.binding(i)
		if err := db.Bindings().Commit(context.Background(), &binding); err != nil {
			t.Fatalf("commit binding for %s: %v", l.bot(i), err)
		}
	}
	h.start(cfg)
	return h
}

// start builds and runs one fleet over the harness's database and adapter
// backend, which is also how a restart reuses both.
func (h *harness) start(cfg *config.Config) {
	h.t.Helper()
	live, err := fleet.New(cfg, fleet.Options{
		Store: h.db.Sessions(), Chats: h.db.Chats(), Peers: h.db.Peers(),
		Messages: h.db.Messages(), Turn: h.db.Turns(), Outbox: h.db.Outbox(),
		Bindings: h.db.Bindings(), Catalog: h.catalog(),
		DefaultTimeout: time.Minute, Now: time.Now,
		OnError: h.recordError,
		Log:     botflota.DiscardLogger(),
	})
	if err != nil {
		h.t.Fatalf("build fleet: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- live.Run(runCtx) }()
	h.fleet, h.cancel, h.done = live, cancel, done
	h.waitRunning()
}

func (h *harness) catalog() *channels.Catalog {
	def := testchannel.Definition()
	deps := testchannel.Deps{Bindings: h.db.Bindings(), Backend: h.backend}
	def.Build = testchannel.Builder(deps)
	def.Prepare = testchannel.Prepare(deps)
	def.Setup = testchannel.Setup(deps)
	catalog, err := channels.New(def)
	if err != nil {
		h.t.Fatalf("build catalog: %v", err)
	}
	return catalog
}

// config is one scope per shard, a graph whose root node loops to itself and
// echoes the inbound body on every turn, and bots bound to the synthetic
// adapter. Echoing the body is what lets a test correlate one reply with the
// exact input that produced it.
func (l layout) config(t *testing.T) *config.Config {
	t.Helper()
	scopes := map[string]spec.Scope{}
	for s := 0; s < l.Scopes; s++ {
		scopes[l.scopeName(s)] = spec.Scope{
			Actions: map[string]spec.Action{"reply": {Steps: []spec.Step{
				{Message: &spec.Message{Body: spec.Text{"{{message.body}}"}}},
			}}},
			Graphs: map[string]spec.Graph{graphName: {
				Root: "echo",
				Nodes: map[string]spec.Node{"echo": {
					OnEnter: []string{"reply"},
					Edges:   []spec.Edge{{Goto: "echo"}},
				}},
			}},
			Bots: map[string]spec.Bot{},
		}
	}
	for i := 0; i < l.Bots; i++ {
		scope := scopes[l.scopeName(l.scopeOf(i))]
		scope.Bots[l.bot(i)] = spec.Bot{
			Graph: graphName,
			Channel: spec.Channel{
				Provider: testchannel.Provider,
				Mode:     testchannel.Mode,
				Options:  l.options(t, i),
			},
			Settings: l.pacing(),
		}
		scopes[l.scopeName(l.scopeOf(i))] = scope
	}
	return &config.Config{DefaultTimeout: 60, Scopes: scopes}
}

func (l layout) options(t *testing.T, i int) spec.ChannelOptions {
	t.Helper()
	var channel spec.Channel
	document := "options:\n  namespace: " + l.namespace(i) + "\n"
	if err := yaml.Unmarshal([]byte(document), &channel); err != nil {
		t.Fatalf("parse channel options: %v", err)
	}
	return channel.Options
}

func (l layout) binding(i int) botflota.Binding {
	return botflota.Binding{
		Scope: l.scopeName(l.scopeOf(i)), Bot: l.bot(i),
		Provider: testchannel.Provider, Adapter: testchannel.Kind,
		Mode: testchannel.Mode, Namespace: l.namespace(i),
		Identity: botflota.IdentityKey{
			Provider: testchannel.Provider, Subject: "acct-" + l.bot(i),
		},
	}
}

// pacing is the outbox configuration every bot shares; nil when the layout
// asks for immediate delivery.
func (l layout) pacing() *spec.BotSettings {
	if l.QueueDelayMS == 0 && l.JitterMS == 0 && l.MaxPerMinute == 0 {
		return nil
	}
	return &spec.BotSettings{
		QueueDelay:      l.QueueDelayMS,
		Jitter:          l.JitterMS,
		MaxMsgPerMinute: l.MaxPerMinute,
	}
}

func (l layout) scopeOf(i int) int { return i % l.Scopes }

func (l layout) scopeName(s int) string { return fmt.Sprintf("s%d", s) }

func (l layout) bot(i int) string { return fmt.Sprintf("b%d", i) }

func (l layout) namespace(i int) string {
	if l.Shared {
		return "ns-shared"
	}
	return "ns-" + l.bot(i)
}

// target is one direct conversation, named after its peer subject.
func (l layout) target(chat int) botflota.ChatTarget {
	return botflota.ChatTarget{
		Provider: testchannel.Provider,
		Kind:     botflota.ChatDirect,
		Thread:   peerName(chat),
	}
}

func peerName(chat int) string { return fmt.Sprintf("peer-%d", chat) }

// channel is one bot's live logical view, the test-side driver and observer.
func (h *harness) channel(i int) *testchannel.Channel {
	h.t.Helper()
	scope, bot := h.layout.scopeName(h.layout.scopeOf(i)), h.layout.bot(i)
	found, ok := h.backend.Lookup(scope, bot)
	if !ok {
		h.t.Fatalf("no channel built for %s/%s", scope, bot)
	}
	return found
}

// inject delivers one inbound turn and fails the test if the fleet stops
// accepting input.
func (h *harness) inject(i, chat int, id string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), h.deadline())
	defer cancel()
	if err := h.injectCtx(ctx, i, chat, id); err != nil {
		h.t.Fatalf("inject %s into %s: %v", id, h.layout.bot(i), err)
	}
}

// injectCtx is the error-returning form a concurrent or long-running driver
// uses, so a failure never reports from a goroutine the testing package did
// not start.
func (h *harness) injectCtx(
	ctx context.Context, i, chat int, id string,
) error {
	msg := botflota.IncomingMessage{
		Bot:               h.layout.bot(i),
		Sender:            botflota.IdentityKey{Provider: testchannel.Provider, Subject: peerName(chat)},
		Target:            h.layout.target(chat),
		Content:           botflota.TextContent{Body: id},
		ProviderMessageID: id,
		At:                time.Now(),
	}
	return h.channel(i).InjectContext(ctx, msg)
}

func (h *harness) deadline() time.Duration {
	if h.layout.Bots > 16 {
		return 90 * time.Second
	}
	return 20 * time.Second
}

func (h *harness) waitRunning() {
	h.t.Helper()
	h.waitFor("every bot to join the running fleet", func() bool {
		running := map[botflota.BotRef]bool{}
		for _, status := range h.fleet.Status() {
			if status.State != "stopped" {
				running[botflota.BotRef{Scope: status.Scope, Bot: status.Bot}] = true
			}
		}
		return len(running) >= h.layout.Bots
	})
}

// stop ends the fleet and waits for its full shutdown within a deadline.
func (h *harness) stop() {
	h.t.Helper()
	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatalf("fleet run: %v", err)
		}
	case <-time.After(h.deadline()):
		h.t.Fatalf("the fleet did not stop within %s", h.deadline())
	}
}

func (h *harness) waitFor(what string, ready func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(h.deadline())
	for {
		if ready() {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settled reports that no inbound is buffered and no outbox entry is pending,
// so the fleet has handled every delivered input and drained its replies.
func (h *harness) settled() bool {
	buffered := 0
	for i := 0; i < h.layout.Bots; i++ {
		buffered += len(h.channel(i).Incoming())
	}
	if buffered > 0 {
		return false
	}
	for _, status := range h.fleet.Status() {
		if status.OutboxDepth > 0 {
			return false
		}
	}
	return true
}

// sentBodies returns the retained reply bodies of one bot, oldest first.
func sentBodies(channel *testchannel.Channel) []string {
	sent := channel.Sent()
	bodies := make([]string, 0, len(sent))
	for _, msg := range sent {
		bodies = append(bodies, botflota.ContentText(msg.Content))
	}
	return bodies
}

// countBodies groups reply bodies with their multiplicity, so a correlated
// assertion can require exactly one reply per input without depending on
// cross-conversation ordering.
func countBodies(bodies []string) map[string]int {
	counts := make(map[string]int, len(bodies))
	for _, body := range bodies {
		counts[body]++
	}
	return counts
}
