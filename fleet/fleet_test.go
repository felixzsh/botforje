package fleet

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

var t0 = time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)

type fakeChannel struct {
	incoming chan botflota.IncomingMessage
	sent     chan botflota.OutgoingMessage

	release chan struct{}
	entered chan struct{}
	once    sync.Once

	mu           sync.Mutex
	connected    bool
	disconnected bool
}

func newFakeChannel() *fakeChannel {
	return &fakeChannel{
		incoming: make(chan botflota.IncomingMessage, 16),
		sent:     make(chan botflota.OutgoingMessage, 16),
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

func (c *fakeChannel) Send(_ context.Context, msg botflota.OutgoingMessage) (string, error) {
	if c.entered != nil {
		c.once.Do(func() { close(c.entered) })
	}
	if c.release != nil {
		<-c.release
	}
	c.sent <- msg
	return "wamid." + botflota.ContentText(msg.Content), nil
}

func (c *fakeChannel) Incoming() <-chan botflota.IncomingMessage { return c.incoming }

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
	mu         sync.Mutex
	senders    map[string]*botflota.Sender
	chats      map[string]*botflota.Chat
	sessions   map[int64]*botflota.Session
	messages   []*botflota.Message
	nextSender int64
	nextChat   int64
	nextSess   int64
	nextMsg    int64
}

func newStore() *memStore {
	return &memStore{
		senders:  map[string]*botflota.Sender{},
		chats:    map[string]*botflota.Chat{},
		sessions: map[int64]*botflota.Session{},
	}
}

func senderKey(scope, phone string) string { return scope + "\x00" + phone }

func sessionKey(scope, bot string, senderID int64) string {
	return scope + "\x00" + bot + "\x00" + strconv.FormatInt(senderID, 10)
}

func (s *memStore) Upsert(
	_ context.Context, scope, phone, name string, at time.Time,
) (*botflota.Sender, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	sender, ok := s.senders[senderKey(scope, phone)]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return sender, nil
}

func (s *memStore) Ensure(
	_ context.Context, scope, bot string, senderID int64, at time.Time,
) (*botflota.Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionKey(scope, bot, senderID)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	chat, ok := s.chats[sessionKey(scope, bot, senderID)]
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chat := range s.chats {
		if chat.ID == chatID {
			chat.Guards = guards
			return nil
		}
	}
	return botflota.ErrNotFound
}

func (s *memStore) SetDisplayName(
	_ context.Context, scope, phone, name string, at time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sender, ok := s.senders[senderKey(scope, phone)]; ok && name != "" {
		sender.DisplayName = name
		sender.NameUpdatedAt = at
	}
	return nil
}

func (s *memStore) NoteHistory(
	_ context.Context, chatID int64, from, to time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chat := range s.chats {
		if chat.ID != chatID {
			continue
		}
		if !from.IsZero() && (chat.StartedAt.IsZero() || from.Before(chat.StartedAt)) {
			chat.StartedAt = from
		}
		if to.After(chat.UpdatedAt) {
			chat.UpdatedAt = to
		}
		return nil
	}
	return botflota.ErrNotFound
}

func (s *memStore) Latest(_ context.Context, chatID int64) (*botflota.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[chatID]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return session, nil
}

func (s *memStore) Save(_ context.Context, session *botflota.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == 0 {
		s.nextSess++
		session.ID = s.nextSess
	}
	s.sessions[session.ChatID] = session
	return nil
}

func (s *memStore) Delete(_ context.Context, chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, chatID)
	return nil
}

func (s *memStore) CountActive(
	_ context.Context, scope, bot string, at time.Time,
) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextMsg++
	message.ID = s.nextMsg
	s.messages = append(s.messages, message)
	return nil
}

func (s *memStore) HasProviderID(
	_ context.Context, chatID int64, providerID string,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if providerID == "" {
		return false, nil
	}
	for _, message := range s.messages {
		if message.ChatID == chatID && message.ProviderMessageID == providerID {
			return true, nil
		}
	}
	return false, nil
}

// CommitTurn applies the turn in order, the way SQLite applies it in one
// transaction: the fake cannot fail halfway, so both shapes stay equivalent.
func (s *memStore) CommitTurn(ctx context.Context, turn botflota.TurnRecord) error {
	if turn.Session == nil {
		return errors.New("memstore: turn has no session")
	}
	if turn.Inbound == nil {
		return errors.New("memstore: turn has no inbound message")
	}
	if err := s.Save(ctx, turn.Session); err != nil {
		return err
	}
	turn.Inbound.SessionID = turn.Session.ID
	if err := s.Append(ctx, turn.Inbound); err != nil {
		return err
	}
	for _, outbound := range turn.Outbound {
		outbound.SessionID = turn.Session.ID
		if err := s.Append(ctx, outbound); err != nil {
			return err
		}
	}
	if err := s.RecordMessage(ctx, turn.ChatID, turn.At); err != nil {
		return err
	}
	if turn.Guards != nil {
		return s.SaveGuards(ctx, turn.ChatID, turn.Guards)
	}
	return nil
}

func (s *memStore) SetProviderID(_ context.Context, id int64, providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, message := range s.messages {
		if message.ID == id {
			message.ProviderMessageID = providerID
			return nil
		}
	}
	return botflota.ErrNotFound
}

func (s *memStore) Oldest(_ context.Context, chatID int64) (*botflota.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldest *botflota.Message
	for _, message := range s.messages {
		if message.ChatID != chatID || message.ProviderMessageID == "" {
			continue
		}
		if oldest == nil || message.At.Before(oldest.At) ||
			(message.At.Equal(oldest.At) && message.ID < oldest.ID) {
			oldest = message
		}
	}
	if oldest == nil {
		return nil, botflota.ErrNotFound
	}
	return oldest, nil
}

func (s *memStore) History(
	_ context.Context, chatID, afterID int64, limit int,
) ([]botflota.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func buildFleet(t *testing.T, blocking string, log *slog.Logger) (*Fleet, map[string]*fakeChannel) {
	t.Helper()
	runner, channels, _ := buildFleetWithStore(t, blocking, log)
	return runner, channels
}

func buildFleetWithStore(
	t *testing.T, blocking string, log *slog.Logger,
) (*Fleet, map[string]*fakeChannel, *memStore) {
	t.Helper()
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if errs := config.Validate(cfg); len(errs) != 0 {
		t.Fatalf("config invalid: %v", errs)
	}
	channels := map[string]*fakeChannel{}
	store := newStore()
	runner, err := New(cfg, Options{
		Store:          store,
		Chats:          store,
		Senders:        store,
		Messages:       store,
		Turn:           store,
		DefaultTimeout: time.Minute,
		Now:            func() time.Time { return t0 },
		Log:            log,
		NewChannel: func(spec BotSpec) (botflota.MessageChannel, error) {
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
	return runner, channels, store
}

type syncChannel struct {
	fakeChannel
	phone string
	count int
}

func (c *syncChannel) SyncHistory(
	_ context.Context, phone string, count int, _ time.Duration,
) (botflota.HistoryResult, error) {
	c.phone, c.count = phone, count
	return botflota.HistoryResult{
		Received: count, Rounds: 1, Imported: count,
	}, nil
}

func TestHistorySyncRoutesToTheChannelThatSupportsIt(t *testing.T) {
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store := newStore()
	channel := &syncChannel{fakeChannel: *newFakeChannel()}
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Senders: store, Messages: store, Turn: store,
		NewChannel: func(BotSpec) (botflota.MessageChannel, error) { return channel, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := runner.HistorySync(
		context.Background(), "dev", "alpha", "5215500000000", 50, time.Second)
	if err != nil {
		t.Fatalf("HistorySync: %v", err)
	}
	if channel.phone != "5215500000000" || channel.count != 50 || result.Received != 50 {
		t.Fatalf("channel = %s/%d, result = %+v", channel.phone, channel.count, result)
	}
	if _, err := runner.HistorySync(
		context.Background(), "dev", "ghost", "5215500000000", 50, time.Second); err == nil {
		t.Fatal("an unknown bot must report an error")
	}
}

func TestHistorySyncRejectsAChannelWithoutTheCapability(t *testing.T) {
	runner, _ := buildFleet(t, "", nil)
	if _, err := runner.HistorySync(
		context.Background(), "dev", "alpha", "5215500000000", 50, time.Second); err == nil {
		t.Fatal("a channel without on-demand history must report an error")
	}
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
	store := newStore()
	runner, err := New(cfg, Options{
		Store:    store,
		Chats:    store,
		Senders:  store,
		Messages: store,
		Turn:     store,
		Skip:     func(_, bot string) bool { return bot == "beta" },
		NewChannel: func(spec BotSpec) (botflota.MessageChannel, error) {
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

// TestStartBotJoinsTheRunningFleet covers a bot paired after startup joining
// the fleet without a restart: the pairing session of the running instance
// activates it (ADR 0022).
func TestStartBotJoinsTheRunningFleet(t *testing.T) {
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store := newStore()
	runner, err := New(cfg, Options{
		Store:    store,
		Chats:    store,
		Senders:  store,
		Messages: store,
		Turn:     store,
		Skip:     func(_, bot string) bool { return bot == "beta" },
		NewChannel: func(BotSpec) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := runner.StartBot("dev", "beta"); err == nil {
		t.Fatal("a fleet that is not running must refuse StartBot")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	// StartBot answers until Run has published the run context.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := runner.StartBot("dev", "beta")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("StartBot: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	waitConnected(t, runner)
	if statuses := runner.Status(); len(statuses) != 2 ||
		statuses[0].Bot != "alpha" || statuses[1].Bot != "beta" {
		t.Fatalf("statuses = %#v, want alpha and beta", statuses)
	}
	if err := runner.StartBot("dev", "beta"); err != nil {
		t.Fatalf("starting a running bot again = %v, want a no-op", err)
	}
	if err := runner.StartBot("dev", "ghost"); err == nil {
		t.Fatal("an unknown bot must report an error")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestApplyConfigAppliesAtTheNextMessage covers the adoption point of a
// reload: the next message runs the reloaded action with no restart
// (ADR 0009).
func TestApplyConfigAppliesAtTheNextMessage(t *testing.T) {
	runner, channels, _ := buildFleetWithStore(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["alpha"].incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "s", Content: botflota.TextContent{Body: "hi"},
	}
	first := receive(t, channels["alpha"].sent)
	if got := botflota.ContentText(first.Content); got != "hello from alpha" {
		t.Fatalf("first reply = %q", got)
	}

	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	scope := cfg.Scopes["dev"]
	scope.Actions["greet"] = spec.Action{Steps: []spec.Step{
		{Message: &spec.Message{Body: spec.Text{"reloaded"}}},
	}}
	cfg.Scopes["dev"] = scope
	runner.ApplyConfig(cfg)

	channels["alpha"].incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "other",
		Content: botflota.TextContent{Body: "hi"},
	}
	if reply := receive(t, channels["alpha"].sent); botflota.ContentText(reply.Content) != "reloaded" {
		t.Fatalf("second reply = %q, want the reloaded action",
			botflota.ContentText(reply.Content))
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

func receive(t *testing.T, messages <-chan botflota.OutgoingMessage) botflota.OutgoingMessage {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("no message was sent")
		return botflota.OutgoingMessage{}
	}
}

func TestTwoBotsAreIndependent(t *testing.T) {
	runner, channels := buildFleet(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["alpha"].incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "s", Content: botflota.TextContent{Body: "hi"},
	}
	reply := receive(t, channels["alpha"].sent)
	if got := botflota.ContentText(reply.Content); got != "hello from alpha" {
		t.Fatalf("alpha reply = %q", got)
	}
	select {
	case other := <-channels["beta"].sent:
		t.Fatalf("beta answered on alpha's message: %q",
			botflota.ContentText(other.Content))
	case <-time.After(50 * time.Millisecond):
	}
}

// TestOutboundProviderIDIsStored covers the dedup loop with a live reply: the
// channel reports its message id and the fleet writes it onto the stored row.
func TestOutboundProviderIDIsStored(t *testing.T) {
	runner, channels, store := buildFleetWithStore(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["alpha"].incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "s", Content: botflota.TextContent{Body: "hi"},
	}
	reply := receive(t, channels["alpha"].sent)
	want := "wamid." + botflota.ContentText(reply.Content)

	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		got := ""
		for _, message := range store.messages {
			if message.Direction == botflota.Outbound && message.ProviderMessageID != "" {
				got = message.ProviderMessageID
			}
		}
		store.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stored provider id = %q, want %q", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFilterDropsIgnoredSender(t *testing.T) {
	runner, channels, store := buildFleetWithStore(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["beta"].incoming <- botflota.IncomingMessage{
		Bot: "beta", SenderPhone: "999", Content: botflota.TextContent{Body: "hi"},
	}
	select {
	case reply := <-channels["beta"].sent:
		t.Fatalf("beta answered an ignored sender: %q", botflota.ContentText(reply.Content))
	case <-time.After(50 * time.Millisecond):
	}

	channels["beta"].incoming <- botflota.IncomingMessage{
		Bot: "beta", SenderPhone: "1", Content: botflota.TextContent{Body: "hi"},
	}
	reply := receive(t, channels["beta"].sent)
	if got := botflota.ContentText(reply.Content); got != "hello from beta" {
		t.Fatalf("beta reply = %q", got)
	}
	if _, err := store.Get(ctx, "dev", "999"); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("an ignored sender was persisted: %v", err)
	}
	if _, err := store.Get(ctx, "dev", "1"); err != nil {
		t.Fatalf("an allowed sender was not persisted: %v", err)
	}
}

func TestShutdownDrainsBeforeDisconnect(t *testing.T) {
	runner, channels := buildFleet(t, "alpha", nil)
	alpha := channels["alpha"]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = runner.Run(ctx); close(done) }()
	waitConnected(t, runner)

	alpha.incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "s", Content: botflota.TextContent{Body: "hi"},
	}
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
	if reply := receive(t, alpha.sent); botflota.ContentText(reply.Content) != "hello from alpha" {
		t.Fatalf("drained reply = %q", botflota.ContentText(reply.Content))
	}
}
