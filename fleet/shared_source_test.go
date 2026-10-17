package fleet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// errViewReleased reports that the shared source gave up handing an entry to a
// logical view because that view was released, not because the entry was lost
// silently.
var errViewReleased = errors.New("shared source: view released")

// sharedSource is one physical backend several logical channel views publish
// through, the shape ADR 0052 describes: it bounds each view's buffer, couples
// consumer progress, and releases one view without closing a peer's view. The
// real adapter owns such a reader; this fake proves the contract the fleet
// relies on.
type sharedSource struct {
	mu    sync.Mutex
	views map[string]*sharedView
	depth int
}

func newSharedSource(depth int) *sharedSource {
	return &sharedSource{views: map[string]*sharedView{}, depth: depth}
}

// build returns a fresh logical view for one bot, the way a channel builder
// assembles a channel per binding. A restart gets a new view, so a stopped
// instance is never reused (ADR 0052).
func (s *sharedSource) build(bot string) *sharedView {
	s.mu.Lock()
	defer s.mu.Unlock()
	view := s.newView()
	s.views[bot] = view
	return view
}

// view returns the current logical view of one bot.
func (s *sharedSource) view(bot string) *sharedView {
	s.mu.Lock()
	defer s.mu.Unlock()
	if found, ok := s.views[bot]; ok {
		return found
	}
	found := s.newView()
	s.views[bot] = found
	return found
}

func (s *sharedSource) newView() *sharedView {
	return &sharedView{
		incoming: make(chan botflota.IncomingMessage, s.depth),
		sent:     make(chan botflota.OutgoingMessage, 16),
		release:  make(chan struct{}),
	}
}

// publish hands one entry to one bot's logical view. A full bounded buffer
// blocks the shared source instead of dropping the entry, and releasing that
// view frees the wait without touching a peer's view (ADR 0040, ADR 0052).
func (s *sharedSource) publish(bot string, msg botflota.IncomingMessage) error {
	view := s.view(bot)
	// A released view declines first: its buffer may still have room, and a
	// dropped message there would be a silent loss.
	select {
	case <-view.release:
		return errViewReleased
	default:
	}
	select {
	case view.incoming <- msg:
		return nil
	case <-view.release:
		return errViewReleased
	}
}

// sharedView is one bot's logical channel over the shared source.
type sharedView struct {
	incoming chan botflota.IncomingMessage
	sent     chan botflota.OutgoingMessage
	release  chan struct{}
	once     sync.Once

	mu        sync.Mutex
	connected bool
	busy      bool
	overlaps  int
}

func (v *sharedView) Connect(context.Context) error {
	v.mu.Lock()
	v.connected = true
	v.mu.Unlock()
	return nil
}

func (v *sharedView) Disconnect(context.Context) error {
	v.mu.Lock()
	v.connected = false
	v.mu.Unlock()
	v.once.Do(func() { close(v.release) })
	return nil
}

func (v *sharedView) Send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	v.mu.Lock()
	if v.busy {
		v.overlaps++
	}
	v.busy = true
	connected := v.connected
	v.mu.Unlock()
	// Open long enough that two sends from one binding would overlap.
	time.Sleep(time.Millisecond)
	v.mu.Lock()
	v.busy = false
	v.mu.Unlock()
	if !connected {
		return "", errors.New("shared view: released")
	}
	v.sent <- msg
	return "wamid." + botflota.ContentText(msg.Content), nil
}

func (v *sharedView) Incoming() <-chan botflota.IncomingMessage { return v.incoming }

func (v *sharedView) ChannelState() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.connected {
		return "connected"
	}
	return "stopped"
}

func (v *sharedView) released() bool {
	select {
	case <-v.release:
		return true
	default:
		return false
	}
}

func (v *sharedView) isConnected() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.connected
}

func (v *sharedView) overlapCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.overlaps
}

// sharedInbound is one neutral entry the shared source publishes to a view.
// Each peer is its own conversation, so the fixture graph answers every one.
func sharedInbound(bot, subject, body string) botflota.IncomingMessage {
	return botflota.IncomingMessage{
		Bot: bot, Sender: waKey(subject), Target: waTarget(subject),
		Content: botflota.TextContent{Body: body}, At: t0,
	}
}

// gatedTurn blocks one bot's turn commits until the test releases them, so a
// deliberately slow consumer can hold up the shared source.
type gatedTurn struct {
	*memStore
	bot     string
	gate    chan struct{}
	once    sync.Once
	entered chan struct{}
}

func (g *gatedTurn) CommitTurn(
	ctx context.Context, turn botflota.TurnRecord,
) error {
	if turn.Bot == g.bot {
		g.once.Do(func() { close(g.entered) })
		select {
		case <-g.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g.memStore.CommitTurn(ctx, turn)
}

func buildSharedFleet(
	t *testing.T, source *sharedSource, store *memStore, turn botflota.TurnStore,
) *Fleet {
	t.Helper()
	if turn == nil {
		turn = store
	}
	runner, err := New(loadFleetConfig(t), Options{
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: turn, Outbox: store,
		DefaultTimeout: time.Minute,
		Now:            func() time.Time { return t0 },
		Catalog: fakeCatalog(
			func(_, bot string, _ any) (botflota.MessageChannel, error) {
				return source.build(bot), nil
			}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

func runSharedFleet(t *testing.T, runner *Fleet) (context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	waitConnected(t, runner)
	return cancel, done
}

// TestSharedSourceRoutesEachViewToItsOwnBinding: two logical views share one
// physical source, and each entry is read once by its own binding and answered
// through it, with no cross-talk (ADR 0051, ADR 0052).
func TestSharedSourceRoutesEachViewToItsOwnBinding(t *testing.T) {
	source := newSharedSource(4)
	runner := buildSharedFleet(t, source, newStore(), nil)
	cancel, done := runSharedFleet(t, runner)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}
	}()

	for _, bot := range []string{"alpha", "beta"} {
		if err := source.publish(bot, sharedInbound(bot, "peer-1", "hi")); err != nil {
			t.Fatalf("publish to %s: %v", bot, err)
		}
	}
	for _, bot := range []string{"alpha", "beta"} {
		reply := receive(t, source.view(bot).sent)
		if want := "hello from " + bot; botflota.ContentText(reply.Content) != want {
			t.Fatalf("%s reply = %q, want %q", bot,
				botflota.ContentText(reply.Content), want)
		}
		if reply.Target != waTarget("peer-1") {
			t.Fatalf("%s reply target = %+v", bot, reply.Target)
		}
		if overlaps := source.view(bot).overlapCount(); overlaps != 0 {
			t.Fatalf("%s overlapped %d sends", bot, overlaps)
		}
		select {
		case extra := <-source.view(bot).sent:
			t.Fatalf("%s produced an extra send: %#v", bot, extra)
		default:
		}
	}
}

// TestSharedSourceAppliesBackpressureInsteadOfDropping: a slow logical consumer
// lets the shared source's bounded buffer fill, so publishing waits instead of
// dropping, and the source resumes as the consumer progresses — coupled
// progress, never a silent loss (ADR 0040, ADR 0052).
func TestSharedSourceAppliesBackpressureInsteadOfDropping(t *testing.T) {
	source := newSharedSource(2)
	store := newStore()
	turn := &gatedTurn{
		memStore: store, bot: "alpha",
		gate: make(chan struct{}), entered: make(chan struct{}),
	}
	runner := buildSharedFleet(t, source, store, turn)
	cancel, done := runSharedFleet(t, runner)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}
	}()

	if err := source.publish("alpha", sharedInbound("alpha", "peer-1", "first")); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	select {
	case <-turn.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the slow consumer never reached its turn")
	}
	// The consumer is stuck inside its turn, so the bounded buffer fills.
	for _, peer := range []string{"peer-2", "peer-3"} {
		if err := source.publish("alpha", sharedInbound("alpha", peer, "hi")); err != nil {
			t.Fatalf("publish %s: %v", peer, err)
		}
	}
	blocked := make(chan error, 1)
	go func() {
		blocked <- source.publish("alpha", sharedInbound("alpha", "peer-4", "hi"))
	}()
	select {
	case err := <-blocked:
		t.Fatalf("a publish returned while the bounded buffer was full: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(turn.gate)
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("blocked publish: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the freed buffer never accepted the blocked publish")
	}
	for _, peer := range []string{"peer-1", "peer-2", "peer-3", "peer-4"} {
		reply := receive(t, source.view("alpha").sent)
		if got := botflota.ContentText(reply.Content); got != "hello from alpha" {
			t.Fatalf("reply for %s = %q, want all four delivered", peer, got)
		}
		if reply.Target != waTarget(peer) {
			t.Fatalf("reply target = %+v, want %+v", reply.Target, waTarget(peer))
		}
	}
}

// TestSharedSourceReleasesOneViewWithoutClosingPeers: stopping one bot releases
// only its own view; the shared source then declines that view instead of
// blocking, and the peer view stays connected and keeps delivering (ADR 0040,
// ADR 0052).
func TestSharedSourceReleasesOneViewWithoutClosingPeers(t *testing.T) {
	source := newSharedSource(2)
	runner := buildSharedFleet(t, source, newStore(), nil)
	cancel, done := runSharedFleet(t, runner)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}
	}()
	alpha, beta := source.view("alpha"), source.view("beta")

	if err := runner.StopBot("dev", "alpha"); err != nil {
		t.Fatalf("StopBot: %v", err)
	}
	if !alpha.released() {
		t.Fatal("the stopped bot did not release its own view")
	}
	if beta.released() {
		t.Fatal("stopping one view released its peer")
	}
	if !beta.isConnected() {
		t.Fatal("the peer view disconnected with its neighbour")
	}

	if err := source.publish("alpha", sharedInbound("alpha", "peer-1", "gone")); !errors.Is(
		err, errViewReleased,
	) {
		t.Fatalf("publish to a released view = %v, want errViewReleased", err)
	}
	if err := source.publish("beta", sharedInbound("beta", "peer-1", "hi")); err != nil {
		t.Fatalf("publish to the peer view: %v", err)
	}
	reply := receive(t, beta.sent)
	if got := botflota.ContentText(reply.Content); got != "hello from beta" {
		t.Fatalf("peer reply = %q, want the peer still answering", got)
	}
}

// TestSharedSourceKeepsThePeerViewUnderRepeatedTeardown: rebuilding one binding
// while its peer keeps receiving never reroutes or closes the peer's view, so
// selecting the rebuilt context is isolated (ADR 0052, ADR 0053).
func TestSharedSourceKeepsThePeerViewUnderRepeatedTeardown(t *testing.T) {
	source := newSharedSource(8)
	runner := buildSharedFleet(t, source, newStore(), nil)
	cancel, done := runSharedFleet(t, runner)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}
	}()

	cycled := make(chan struct{})
	cycleErr := make(chan error, 1)
	go func() {
		defer close(cycled)
		for i := 0; i < 5; i++ {
			if err := runner.StopBot("dev", "alpha"); err != nil {
				cycleErr <- err
				return
			}
			if err := runner.StartBot("dev", "alpha"); err != nil {
				cycleErr <- err
				return
			}
		}
	}()

	beta := source.view("beta")
	for i := 0; i < 6; i++ {
		peer := fmt.Sprintf("peer-%d", i)
		if err := source.publish("beta", sharedInbound("beta", peer, "hi")); err != nil {
			t.Fatalf("publish %s: %v", peer, err)
		}
		reply := receive(t, beta.sent)
		if got := botflota.ContentText(reply.Content); got != "hello from beta" {
			t.Fatalf("peer reply %d = %q", i, got)
		}
		if reply.Target != waTarget(peer) {
			t.Fatalf("peer reply target = %+v, want %+v", reply.Target, waTarget(peer))
		}
	}
	<-cycled
	select {
	case err := <-cycleErr:
		t.Fatalf("cycling the peer binding: %v", err)
	default:
	}
	if !beta.isConnected() || beta.released() {
		t.Fatal("rebuilding one binding disturbed its peer's view")
	}
}
