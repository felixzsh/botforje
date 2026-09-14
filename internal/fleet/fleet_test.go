package fleet

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botforje/internal/config"
	"github.com/felixzsh/botforje/internal/domain"
	"github.com/felixzsh/botforje/internal/logging"
)

// safeBuffer is an io.Writer the fleet goroutine and the test share.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var t0 = time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)

type fakeChannel struct {
	incoming chan domain.IncomingMessage
	sent     chan domain.OutgoingMessage

	release chan struct{}
	entered chan struct{}
	once    sync.Once

	mu           sync.Mutex
	connected    bool
	disconnected bool
}

func newFakeChannel() *fakeChannel {
	return &fakeChannel{
		incoming: make(chan domain.IncomingMessage, 16),
		sent:     make(chan domain.OutgoingMessage, 16),
	}
}

func (c *fakeChannel) Connect(context.Context) error {
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	return nil
}

func (c *fakeChannel) Disconnect(context.Context) error {
	c.mu.Lock()
	c.connected = false
	c.disconnected = true
	c.mu.Unlock()
	return nil
}

func (c *fakeChannel) Send(_ context.Context, msg domain.OutgoingMessage) error {
	if c.entered != nil {
		c.once.Do(func() { close(c.entered) })
	}
	if c.release != nil {
		<-c.release
	}
	c.sent <- msg
	return nil
}

func (c *fakeChannel) Incoming() <-chan domain.IncomingMessage { return c.incoming }

func (c *fakeChannel) ChannelState() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return "connected"
	}
	return "stopped"
}

func (c *fakeChannel) wasDisconnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnected
}

type memStore struct {
	mu       sync.Mutex
	sessions map[string]*domain.Session
}

func newStore() *memStore { return &memStore{sessions: map[string]*domain.Session{}} }

func sessionKey(scope, bot, sender string) string {
	return scope + "\x00" + bot + "\x00" + sender
}

func (s *memStore) Get(_ context.Context, scope, bot, sender string) (*domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionKey(scope, bot, sender)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return session, nil
}

func (s *memStore) Save(_ context.Context, session *domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionKey(session.Scope, session.Bot, session.Sender)] = session
	return nil
}

func (s *memStore) Delete(_ context.Context, scope, bot, sender string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionKey(scope, bot, sender))
	return nil
}

func buildFleet(t *testing.T, blocking string, log *slog.Logger) (*Fleet, map[string]*fakeChannel) {
	t.Helper()
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if errs := config.Validate(cfg); len(errs) != 0 {
		t.Fatalf("config invalid: %v", errs)
	}
	channels := map[string]*fakeChannel{}
	runner, err := New(cfg, Options{
		Store:          newStore(),
		DefaultTimeout: time.Minute,
		Now:            func() time.Time { return t0 },
		Log:            log,
		NewChannel: func(spec BotSpec) (domain.MessageChannel, error) {
			channel := newFakeChannel()
			if spec.Name == blocking {
				channel.release = make(chan struct{})
				channel.entered = make(chan struct{})
			}
			channels[spec.Name] = channel
			return channel, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, channels
}

func TestSkipOmitsBotBeforeChannelCreation(t *testing.T) {
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if errs := config.Validate(cfg); len(errs) != 0 {
		t.Fatalf("config invalid: %v", errs)
	}
	var created []string
	runner, err := New(cfg, Options{
		Store: newStore(),
		Skip:  func(_, bot string) bool { return bot == "beta" },
		NewChannel: func(spec BotSpec) (domain.MessageChannel, error) {
			created = append(created, spec.Name)
			return newFakeChannel(), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(created) != 1 || created[0] != "alpha" {
		t.Fatalf("created channels = %v, want [alpha]", created)
	}
	if statuses := runner.Status(); len(statuses) != 1 || statuses[0].Bot != "alpha" {
		t.Fatalf("statuses = %#v, want only alpha", statuses)
	}
}

func waitConnected(t *testing.T, runner *Fleet) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		for _, status := range runner.Status() {
			if status.State != "connected" {
				ready = false
			}
		}
		if ready {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("bots did not connect in time")
}

func receive(t *testing.T, messages <-chan domain.OutgoingMessage) domain.OutgoingMessage {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("no message was sent")
		return domain.OutgoingMessage{}
	}
}

func TestTwoBotsAreIndependent(t *testing.T) {
	runner, channels := buildFleet(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["alpha"].incoming <- domain.IncomingMessage{Bot: "alpha", Sender: "s", Body: "hi"}
	if reply := receive(t, channels["alpha"].sent); reply.Body != "hello from alpha" {
		t.Fatalf("alpha reply = %q", reply.Body)
	}
	select {
	case other := <-channels["beta"].sent:
		t.Fatalf("beta answered on alpha's message: %q", other.Body)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFilterDropsIgnoredSender(t *testing.T) {
	runner, channels := buildFleet(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["beta"].incoming <- domain.IncomingMessage{
		Bot: "beta", Sender: "999", Body: "hi",
	}
	select {
	case reply := <-channels["beta"].sent:
		t.Fatalf("beta answered an ignored sender: %q", reply.Body)
	case <-time.After(50 * time.Millisecond):
	}

	channels["beta"].incoming <- domain.IncomingMessage{
		Bot: "beta", Sender: "1", Body: "hi",
	}
	if reply := receive(t, channels["beta"].sent); reply.Body != "hello from beta" {
		t.Fatalf("beta reply = %q", reply.Body)
	}
}

func TestLogsIncomingBodyAtDebug(t *testing.T) {
	var buffer safeBuffer
	runner, channels := buildFleet(t, "", logging.New("debug", &buffer))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["alpha"].incoming <- domain.IncomingMessage{
		Bot: "alpha", Sender: "s", Body: "hello there",
	}
	receive(t, channels["alpha"].sent)
	if !strings.Contains(buffer.String(), `body="hello there"`) {
		t.Fatalf("log did not include the incoming body: %q", buffer.String())
	}
}

func TestShutdownDrainsBeforeDisconnect(t *testing.T) {
	runner, channels := buildFleet(t, "alpha", nil)
	alpha := channels["alpha"]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = runner.Run(ctx); close(done) }()
	waitConnected(t, runner)

	alpha.incoming <- domain.IncomingMessage{Bot: "alpha", Sender: "s", Body: "hi"}
	<-alpha.entered
	cancel()

	time.Sleep(50 * time.Millisecond)
	if alpha.wasDisconnected() {
		t.Fatal("the channel disconnected before the queued message was sent")
	}
	close(alpha.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the queue drained")
	}
	if !alpha.wasDisconnected() {
		t.Fatal("the channel was not disconnected after draining")
	}
	if reply := receive(t, alpha.sent); reply.Body != "hello from alpha" {
		t.Fatalf("drained reply = %q", reply.Body)
	}
}
