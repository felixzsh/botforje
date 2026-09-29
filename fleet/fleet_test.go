package fleet

import (
	"context"
	"errors"
	"log/slog"
	"slices"
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

	connectEntered chan struct{}
	connectRelease chan struct{}
	connectErr     error
	connectOnce    sync.Once

	mu           sync.Mutex
	connected    bool
	disconnected bool
	sendCalls    int
}

func newFakeChannel() *fakeChannel {
	return &fakeChannel{
		incoming: make(chan botflota.IncomingMessage, 16),
		sent:     make(chan botflota.OutgoingMessage, 16),
	}
}

func (c *fakeChannel) Connect(ctx context.Context) error {
	if c.connectEntered != nil {
		c.connectOnce.Do(func() { close(c.connectEntered) })
	}
	if c.connectRelease != nil {
		select {
		case <-c.connectRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if c.connectErr != nil {
		return c.connectErr
	}
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
	c.mu.Lock()
	c.sendCalls++
	connected := c.connected
	c.mu.Unlock()
	if c.entered != nil {
		c.once.Do(func() { close(c.entered) })
	}
	if !connected {
		return "", errors.New("not connected")
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

func (c *fakeChannel) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendCalls
}

type memStore struct {
	mu         sync.Mutex
	senders    map[string]*botflota.Sender
	chats      map[string]*botflota.Chat
	sessions   map[int64]*botflota.Session
	messages   []*botflota.Message
	outbox     []queuedReply
	nextSender int64
	nextChat   int64
	nextSess   int64
	nextMsg    int64
	nextOut    int64
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

// List is not part of these tests; the scope read is proven against the
// SQLite store.
func (s *memStore) List(
	context.Context, string, time.Time, int, string,
) (botflota.SenderPage, error) {
	return botflota.SenderPage{}, errors.New("memstore: List is not implemented")
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

func (s *memStore) HasActiveSessionOutsideNodes(
	ctx context.Context, scope, bot string, nodes []string, at time.Time,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chat := range s.chats {
		if chat.Scope != scope || chat.Bot != bot {
			continue
		}
		if session, ok := s.sessions[chat.ID]; ok && !session.Expired(at) {
			if !slices.Contains(nodes, session.Node) {
				return true, nil
			}
		}
	}
	return false, nil
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
// transaction, and queues every reply: the engine fake cannot fail halfway,
// and the durable queue itself is proven by the SQLite stores. The queue the
// SQLite adapter writes is emulated here so the worker can be driven in tests.
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
	for i := range turn.Outbound {
		outbound := turn.Outbound[i]
		if outbound.Message == nil {
			return errors.New("memstore: turn has an empty reply")
		}
		if outbound.Recipient == "" {
			return errors.New("memstore: reply has no recipient")
		}
		outbound.Message.SessionID = turn.Session.ID
		if err := s.Append(ctx, outbound.Message); err != nil {
			return err
		}
		content, ok := outbound.Message.Content.(botflota.SendableContent)
		if !ok {
			return errors.New("memstore: reply is not sendable")
		}
		s.nextOut++
		s.outbox = append(s.outbox, queuedReply{
			scope: turn.Scope, bot: turn.Bot,
			entry: botflota.OutboxEntry{
				ID: s.nextOut, Recipient: outbound.Recipient,
				MessageID: outbound.Message.ID, Content: content,
				Origin:  outbound.Message.Origin,
				Routing: outbound.Message.Routing,
			},
		})
	}
	if err := s.RecordMessage(ctx, turn.ChatID, turn.At); err != nil {
		return err
	}
	if turn.Guards != nil {
		return s.SaveGuards(ctx, turn.ChatID, turn.Guards)
	}
	return nil
}

// Record stores a message outside a routable turn and moves its chat activity,
// the way the SQLite turn store does (ADR 0043).
func (s *memStore) Record(
	ctx context.Context, message *botflota.Message, at time.Time,
) error {
	if message == nil {
		return errors.New("memstore: record has no message")
	}
	if err := s.Append(ctx, message); err != nil {
		return err
	}
	if message.Direction == botflota.Inbound {
		return s.RecordMessage(ctx, message.ChatID, at)
	}
	return nil
}

// queuedReply mirrors one outbox row for the worker tests.
type queuedReply struct {
	scope, bot  string
	entry       botflota.OutboxEntry
	nextAttempt time.Time
	sent        bool
	failed      bool
}

func (s *memStore) NextPending(
	_ context.Context, scope, bot string, limit int,
) ([]botflota.OutboxEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var entries []botflota.OutboxEntry
	for _, queued := range s.outbox {
		if len(entries) >= limit {
			break
		}
		if queued.scope != scope || queued.bot != bot || queued.sent || queued.failed {
			continue
		}
		entry := queued.entry
		entry.NextAttempt = queued.nextAttempt
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *memStore) findQueued(id int64) *queuedReply {
	for i := range s.outbox {
		if s.outbox[i].entry.ID == id {
			return &s.outbox[i]
		}
	}
	return nil
}

func (s *memStore) MarkSent(
	_ context.Context, id int64, providerID string, _ time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := s.findQueued(id)
	if queued == nil || queued.sent || queued.failed {
		return botflota.ErrNotFound
	}
	queued.sent = true
	queued.entry.Attempts++
	if providerID != "" {
		for _, message := range s.messages {
			if message.ID == queued.entry.MessageID {
				message.ProviderMessageID = providerID
				break
			}
		}
	}
	return nil
}

func (s *memStore) MarkRetry(
	_ context.Context, id int64, _ string, nextAttempt, _ time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := s.findQueued(id)
	if queued == nil || queued.sent || queued.failed {
		return botflota.ErrNotFound
	}
	queued.entry.Attempts++
	queued.nextAttempt = nextAttempt
	return nil
}

func (s *memStore) MarkFailed(
	_ context.Context, id int64, _ string, _ time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := s.findQueued(id)
	if queued == nil || queued.sent || queued.failed {
		return botflota.ErrNotFound
	}
	queued.failed = true
	queued.entry.Attempts++
	return nil
}

func (s *memStore) Depth(_ context.Context, scope, bot string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	depth := 0
	for _, queued := range s.outbox {
		if queued.scope == scope && queued.bot == bot && !queued.sent && !queued.failed {
			depth++
		}
	}
	return depth, nil
}

func (s *memStore) FailPending(
	_ context.Context, scope, bot, _ string, _ time.Time,
) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	failed := 0
	for i := range s.outbox {
		queued := &s.outbox[i]
		if queued.scope == scope && queued.bot == bot &&
			!queued.sent && !queued.failed {
			queued.failed = true
			failed++
		}
	}
	return failed, nil
}

func (s *memStore) FailPendingForChat(
	_ context.Context, chatID int64, _ string, _ time.Time,
) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := map[int64]bool{}
	for _, message := range s.messages {
		if message.ChatID == chatID {
			stored[message.ID] = true
		}
	}
	failed := 0
	for i := range s.outbox {
		queued := &s.outbox[i]
		if queued.sent || queued.failed || !stored[queued.entry.MessageID] {
			continue
		}
		queued.failed = true
		failed++
	}
	return failed, nil
}

func (s *memStore) IsPending(_ context.Context, id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := s.findQueued(id)
	return queued != nil && !queued.sent && !queued.failed, nil
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
		Outbox:         store,
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
		Outbox:     store,
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
		Outbox:   store,
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
		Outbox:   store,
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

// TestEmptyFleetLivesUntilTheInstanceStops: an instance that starts without
// a paired bot still runs, accepts its first pairing through the fleet,
// and ends only when its context does (ADR 0022).
func TestEmptyFleetLivesUntilTheInstanceStops(t *testing.T) {
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
		Outbox:   store,
		Skip:     func(_, _ string) bool { return true },
		NewChannel: func(BotSpec) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	// The pairing session of a running instance may start the first bot:
	// StartBot answers until Run has published the run context.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := runner.StartBot("dev", "alpha")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("StartBot: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	waitConnected(t, runner)
	select {
	case err := <-done:
		t.Fatalf("Run returned while the instance was live: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestStoppingTheLastBotKeepsTheFleetRunning: unpairing the last bot stops
// that bot, not the instance; the fleet keeps serving and a re-pair joins it
// again without a restart (ADR 0021, ADR 0022).
func TestStoppingTheLastBotKeepsTheFleetRunning(t *testing.T) {
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
		Outbox:   store,
		Skip:     func(_, bot string) bool { return bot == "beta" },
		NewChannel: func(BotSpec) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	waitConnected(t, runner)

	if err := runner.StopBot("dev", "alpha"); err != nil {
		t.Fatalf("StopBot: %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned after the last bot stopped: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := runner.StartBot("dev", "alpha"); err != nil {
		t.Fatalf("re-pair into the running fleet: %v", err)
	}
	waitConnected(t, runner)
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
	applied, pending, err := runner.ApplyConfig(
		context.Background(), cfg, []string{"dev"}, nil)
	if err != nil || len(pending) > 0 || len(applied) != 1 {
		t.Fatalf("ApplyConfig = %v, %v, %v", applied, pending, err)
	}

	channels["alpha"].incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "other",
		Content: botflota.TextContent{Body: "hi"},
	}
	if reply := receive(t, channels["alpha"].sent); botflota.ContentText(reply.Content) != "reloaded" {
		t.Fatalf("second reply = %q, want the reloaded action",
			botflota.ContentText(reply.Content))
	}
}

func TestApplyConfigDefersGraphTopologyWithLiveSession(t *testing.T) {
	runner, channels, _ := buildFleetWithStore(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	channels["alpha"].incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "active",
		Content: botflota.TextContent{Body: "hi"},
	}
	receive(t, channels["alpha"].sent)

	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	scope := cfg.Scopes["dev"]
	graph := scope.Graphs["menu"]
	graph.Nodes["menu"] = spec.Node{Edges: []spec.Edge{{
		Intent: "continue", Goto: "next",
	}}}
	graph.Nodes["next"] = spec.Node{}
	scope.Graphs["menu"] = graph
	cfg.Scopes["dev"] = scope

	applied, pending, err := runner.ApplyConfig(ctx, cfg, []string{"dev"}, nil)
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if len(applied) != 0 || len(pending["dev"]) == 0 {
		t.Fatalf("ApplyConfig = %v, %v; want dev pending", applied, pending)
	}
	runner.mu.Lock()
	gen := runner.scopeGen["dev"]
	runner.mu.Unlock()
	if gen != 0 {
		t.Fatalf("scope generation = %d, want unchanged", gen)
	}
	cancel()
}

func TestApplyConfigLeavesOtherScopeRuntimeUntouched(t *testing.T) {
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	other := cfg.Scopes["dev"]
	other.Bots = map[string]spec.Bot{"gamma": other.Bots["alpha"]}
	cfg.Scopes["other"] = other
	store := newStore()
	channels := map[string]*fakeChannel{}
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Senders: store, Messages: store,
		Turn: store, Outbox: store, DefaultTimeout: time.Minute,
		Now: func() time.Time { return t0 },
		NewChannel: func(bot BotSpec) (botflota.MessageChannel, error) {
			channel := newFakeChannel()
			channels[bot.Scope+"/"+bot.Name] = channel
			return channel, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)

	for _, target := range []struct{ scope, bot string }{
		{"dev", "alpha"}, {"other", "gamma"},
	} {
		channels[target.scope+"/"+target.bot].incoming <- botflota.IncomingMessage{
			Bot: target.bot, SenderPhone: "before-" + target.bot,
			Content: botflota.TextContent{Body: "hi"},
		}
		receive(t, channels[target.scope+"/"+target.bot].sent)
	}

	candidate, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load candidate: %v", err)
	}
	candidate.Scopes["other"] = other
	changed := candidate.Scopes["dev"]
	changed.Actions["greet"] = spec.Action{Steps: []spec.Step{
		{Message: &spec.Message{Body: spec.Text{"changed in dev"}}},
	}}
	candidate.Scopes["dev"] = changed

	applied, pending, err := runner.ApplyConfig(ctx, candidate, []string{"dev"}, nil)
	if err != nil || len(pending) != 0 || !slices.Equal(applied, []string{"dev"}) {
		t.Fatalf("ApplyConfig = %v, %v, %v", applied, pending, err)
	}
	runner.mu.Lock()
	devGen, otherGen := runner.scopeGen["dev"], runner.scopeGen["other"]
	runner.mu.Unlock()
	if devGen != 1 || otherGen != 0 {
		t.Fatalf("scope generations = dev:%d other:%d", devGen, otherGen)
	}

	channels["other/gamma"].incoming <- botflota.IncomingMessage{
		Bot: "gamma", SenderPhone: "after-gamma",
		Content: botflota.TextContent{Body: "hi"},
	}
	got := botflota.ContentText(receive(t, channels["other/gamma"].sent).Content)
	if got != "hello from gamma" {
		t.Fatalf("other scope reply = %q, want unchanged config", got)
	}
	cancel()
}

func TestNewSkipsBotWithSessionAtRemovedNode(t *testing.T) {
	cfg, err := (config.Loader{}).Load("testdata/two-bots.yml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store := newStore()
	store.chats["active"] = &botflota.Chat{
		ID: 1, Scope: "dev", Bot: "alpha",
	}
	store.sessions[1] = &botflota.Session{
		ChatID: 1, Node: "removed", ExpiresAt: time.Now().Add(time.Hour),
	}
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Senders: store, Messages: store,
		Turn: store, Outbox: store,
		NewChannel: func(BotSpec) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	foundPeer := false
	for _, bot := range runner.Status() {
		if bot.Scope == "dev" && bot.Bot == "alpha" {
			t.Fatal("bot with a session at a removed node was initialized")
		}
		if bot.Scope == "dev" && bot.Bot == "beta" {
			foundPeer = true
		}
	}
	if !foundPeer {
		t.Fatal("an unrelated bot was not initialized")
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

// TestStopBotStopsAndStartBotRebuilds covers the lifecycle the account
// operations need: a stopped bot drains, disconnects, and reports stopped,
// and a later start builds a fresh channel — after a re-pair the device
// store it was built with is gone (ADR 0021, ADR 0022).
func TestStopBotStopsAndStartBotRebuilds(t *testing.T) {
	runner, channels := buildFleet(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runner.Run(ctx)
	waitConnected(t, runner)
	first := channels["alpha"]

	if err := runner.StopBot("dev", "alpha"); err != nil {
		t.Fatalf("StopBot: %v", err)
	}
	if !first.wasDisconnected() {
		t.Fatal("the stopped bot did not disconnect its channel")
	}
	for _, status := range runner.Status() {
		if status.Bot == "alpha" && status.State != "stopped" {
			t.Fatalf("stopped bot reports %q", status.State)
		}
	}
	// A second stop is a no-op, not a double close.
	if err := runner.StopBot("dev", "alpha"); err != nil {
		t.Fatalf("second StopBot: %v", err)
	}

	if err := runner.StartBot("dev", "alpha"); err != nil {
		t.Fatalf("StartBot: %v", err)
	}
	if channels["alpha"] == first {
		t.Fatal("the restarted bot reused its old channel")
	}
	waitConnected(t, runner)
}

// TestStopBotWithoutRunIsANoop: a bot that never started is already stopped.
func TestStopBotWithoutRunIsANoop(t *testing.T) {
	runner, _ := buildFleet(t, "", nil)
	if err := runner.StopBot("dev", "alpha"); err != nil {
		t.Fatalf("StopBot before Run: %v", err)
	}
	if err := runner.StopBot("dev", "ghost"); err != nil {
		t.Fatalf("StopBot of an unknown bot: %v", err)
	}
}

// TestStopBotWaitsForTheQueuedSend: stopping a bot drains what is already
// queued before the channel disconnects, and StopBot answers only after that
// shutdown finished, because the credentials go away next (ADR 0011, ADR
// 0021).
func TestStopBotWaitsForTheQueuedSend(t *testing.T) {
	runner, channels := buildFleet(t, "alpha", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan struct{})
	go func() { _ = runner.Run(ctx); close(runDone) }()
	waitConnected(t, runner)
	alpha := channels["alpha"]

	alpha.incoming <- botflota.IncomingMessage{
		Bot: "alpha", SenderPhone: "s", Content: botflota.TextContent{Body: "hi"},
	}
	<-alpha.entered // the outbox worker is inside the send

	stopped := make(chan error, 1)
	go func() { stopped <- runner.StopBot("dev", "alpha") }()
	select {
	case err := <-stopped:
		t.Fatalf("StopBot answered while the send was in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if alpha.wasDisconnected() {
		t.Fatal("the channel disconnected while a queued send was in flight")
	}

	close(alpha.release)
	if err := <-stopped; err != nil {
		t.Fatalf("StopBot: %v", err)
	}
	if !alpha.wasDisconnected() {
		t.Fatal("StopBot answered before the channel disconnected")
	}
	if reply := receive(t, alpha.sent); botflota.ContentText(reply.Content) != "hello from alpha" {
		t.Fatalf("drained reply = %q, want the queued send to finish",
			botflota.ContentText(reply.Content))
	}
	cancel()
	<-runDone
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
