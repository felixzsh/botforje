// Package whatsapp implements the MessageChannel port with whatsmeow
// (ADR 0001, ADR 0004). It owns the device store, reconnection, dedup, sender
// resolution, and presence (ADR 0011, ADR 0013).
package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	walog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botforje/internal/domain"
	"github.com/felixzsh/botforje/internal/logging"
)

// State is the channel lifecycle reported to the fleet (ADR 0011).
type State string

const (
	StateStopped    State = "stopped"
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateLoggedOut  State = "logged_out"
	StateBanned     State = "banned"
)

// Settings controls presence and pacing for one bot (ADR 0011).
type Settings struct {
	ReadReceipts   bool
	SimulateTyping bool
	TypingDelay    time.Duration
}

// Options configures an Adapter. Bot, Scope, DB, and Accounts are required.
type Options struct {
	Scope    string
	Bot      string
	DB       *sql.DB
	Accounts domain.AccountStore
	Settings Settings
	Log      *slog.Logger
	// DepsLogLevel is the minimum level for logs forwarded from dependencies
	// (whatsmeow). The zero value is info.
	DepsLogLevel slog.Level
	Queue        int
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	Now          func() time.Time
	Rand         *rand.Rand
	// OnState is called when the channel lifecycle changes, for status.
	OnState func(State)
}

// Adapter is the whatsmeow implementation of domain.MessageChannel for one
// bot. Create it with New, then Connect.
type Adapter struct {
	scope     string
	bot       string
	accounts  domain.AccountStore
	container *sqlstore.Container
	settings  Settings
	log       *slog.Logger
	waLogger  walog.Logger
	backoff   *Backoff
	now       func() time.Time
	onState   func(State)

	incoming chan domain.IncomingMessage

	mu     sync.Mutex
	client *whatsmeow.Client
	state  State
	seen   *dedup
	last   map[string]lastInbound

	reconnect chan struct{}
	stop      chan struct{}
	stopped   chan struct{}
	stopOnce  sync.Once
}

type lastInbound struct {
	id     types.MessageID
	chat   types.JID
	sender types.JID
}

// New builds the adapter and its device store on the shared connection.
func New(opts Options) (*Adapter, error) {
	if opts.DB == nil {
		return nil, errors.New("whatsapp: no database")
	}
	var (
		log   = opts.Log
		now   = opts.Now
		rnd   = opts.Rand
		queue = opts.Queue
	)
	if log == nil {
		log = logging.Discard()
	}
	if now == nil {
		now = time.Now
	}
	if rnd == nil {
		rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if queue <= 0 {
		queue = 64
	}
	waLogger := newWaLog(log, opts.DepsLogLevel)
	a := &Adapter{
		scope:     opts.Scope,
		bot:       opts.Bot,
		accounts:  opts.Accounts,
		settings:  opts.Settings,
		log:       log,
		waLogger:  waLogger,
		backoff:   NewBackoff(opts.MinBackoff, opts.MaxBackoff, rnd),
		now:       now,
		onState:   opts.OnState,
		incoming:  make(chan domain.IncomingMessage, queue),
		state:     StateStopped,
		seen:      newDedup(2048),
		last:      map[string]lastInbound{},
		reconnect: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	a.container = sqlstore.NewWithDB(opts.DB, "sqlite", a.waLogger)
	if err := a.container.Upgrade(context.Background()); err != nil {
		return nil, fmt.Errorf("whatsapp: device store: %w", err)
	}
	go a.loop()
	return a, nil
}

// Connect loads the paired device and starts the client.
func (a *Adapter) Connect(ctx context.Context) error {
	account, err := a.accounts.Get(ctx, a.scope, a.bot)
	if err != nil {
		return fmt.Errorf("whatsapp: bot %q is not paired (use pair): %w", a.bot, err)
	}
	jid, err := types.ParseJID(account.JID)
	if err != nil {
		return fmt.Errorf("whatsapp: stored account %s: %w", Number(account.JID), err)
	}
	device, err := a.container.GetDevice(ctx, jid)
	if err != nil {
		return fmt.Errorf("whatsapp: load device %s: %w", Number(account.JID), err)
	}
	if device == nil {
		if err := a.updatePairingStatus(domain.PairingUnpaired); err != nil {
			return err
		}
		return missingDeviceError(account)
	}
	client := whatsmeow.NewClient(device, a.waLogger)
	// Reconnection is ours so it uses the capped backoff with jitter (ADR 0011).
	client.DisableLoginAutoReconnect = true
	client.AddEventHandler(a.handleEvent)

	a.mu.Lock()
	a.client = client
	a.mu.Unlock()
	a.setState(StateConnecting)

	if err := client.Connect(); err != nil {
		return fmt.Errorf("whatsapp: connect: %w", err)
	}
	return nil
}

// Disconnect stops the client and the reconnect loop.
func (a *Adapter) Disconnect(context.Context) error {
	a.mu.Lock()
	client := a.client
	a.client = nil
	a.mu.Unlock()
	if client != nil {
		client.Disconnect()
	}
	a.stopOnce.Do(func() { close(a.stop) })
	<-a.stopped
	a.setState(StateStopped)
	return nil
}

// Send delivers one message, marking the inbound read and showing composing
// presence first when configured (ADR 0011). The recipient is a plain phone
// number; the adapter maps it to the JID the message arrived from, or to the
// phone-number JID when that is unknown (ADR 0020).
func (a *Adapter) Send(ctx context.Context, msg domain.OutgoingMessage) error {
	a.mu.Lock()
	client := a.client
	last := a.last[msg.Recipient]
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return errors.New("whatsapp: not connected")
	}
	if msg.Recipient == "" {
		return errors.New("whatsapp: message has no recipient")
	}
	to := last.chat
	if to.IsEmpty() {
		to = types.NewJID(msg.Recipient, types.DefaultUserServer)
	}
	if a.settings.ReadReceipts && !last.chat.IsEmpty() {
		if err := client.MarkRead(ctx, []types.MessageID{last.id}, a.now(),
			last.chat, last.sender); err != nil {
			a.log.Warn("read receipt failed", "error", err)
		}
	}
	content, err := buildContent(msg)
	if err != nil {
		return err
	}
	composing := a.compose(ctx, client, to)
	_, err = client.SendMessage(ctx, to, content)
	if composing {
		_ = client.SendChatPresence(ctx, to, types.ChatPresencePaused,
			types.ChatPresenceMediaText)
	}
	return err
}

// Incoming is the stream of mapped inbound messages.
func (a *Adapter) Incoming() <-chan domain.IncomingMessage { return a.incoming }

// State reports the current lifecycle state.
func (a *Adapter) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// ChannelState reports the lifecycle state as a string (domain.StateReporter).
func (a *Adapter) ChannelState() string { return string(a.State()) }

func (a *Adapter) compose(
	ctx context.Context, client *whatsmeow.Client, to types.JID,
) bool {
	if !a.settings.SimulateTyping {
		return false
	}
	err := client.SendChatPresence(ctx, to, types.ChatPresenceComposing,
		types.ChatPresenceMediaText)
	if err != nil {
		a.log.Warn("composing presence failed", "error", err)
		return false
	}
	timer := time.NewTimer(a.settings.TypingDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return true
	}
}

func (a *Adapter) setState(state State) {
	a.mu.Lock()
	changed := a.state != state
	a.state = state
	a.mu.Unlock()
	if !changed {
		return
	}
	switch state {
	case StateLoggedOut, StateBanned:
		a.log.Warn("channel state", "state", string(state))
	default:
		a.log.Info("channel state", "state", string(state))
	}
	if state == StateConnected {
		if err := a.updatePairingStatus(domain.PairingPaired); err != nil {
			a.logPairingStatusError(err)
		}
	} else if state == StateLoggedOut {
		if err := a.updatePairingStatus(domain.PairingUnpaired); err != nil {
			a.logPairingStatusError(err)
		}
	}
	if a.onState != nil {
		a.onState(state)
	}
}

func (a *Adapter) updatePairingStatus(state domain.PairingState) error {
	if a.accounts == nil {
		return nil
	}
	return updatePairingStatus(context.Background(), a.accounts, a.scope, a.bot,
		state, pairingNow(a.now))
}

func (a *Adapter) logPairingStatusError(err error) {
	if a.log != nil {
		a.log.Error("persist pairing status", "error", err)
	}
}

func (a *Adapter) handleEvent(raw any) {
	switch event := raw.(type) {
	case *events.Message:
		a.onMessage(event)
	case *events.Connected:
		a.backoff.Reset()
		a.setState(StateConnected)
	case *events.Disconnected:
		a.setState(StateConnecting)
		a.signalReconnect()
	case *events.LoggedOut:
		a.setState(StateLoggedOut)
		a.stopReconnecting()
	case *events.TemporaryBan:
		a.setState(StateBanned)
		a.stopReconnecting()
	case *events.StreamReplaced:
		a.setState(StateStopped)
		a.stopReconnecting()
	}
}

func (a *Adapter) onMessage(event *events.Message) {
	if event.Info.IsFromMe {
		return
	}
	body := messageText(event.Message)
	if body == "" {
		return
	}
	id := string(event.Info.ID)
	if !a.seen.fresh(id) {
		return
	}
	pn, ok := resolveSender(event.Info, a.lookupPN)
	if !ok {
		a.log.Warn("dropping message: sender has no known phone number")
		return
	}
	sender := pn.User
	chat := event.Info.Chat

	a.mu.Lock()
	a.last[sender] = lastInbound{
		id: event.Info.ID, chat: chat, sender: event.Info.Sender,
	}
	a.mu.Unlock()

	message := domain.IncomingMessage{
		Bot:     a.bot,
		Sender:  sender,
		Body:    body,
		ID:      id,
		IsGroup: event.Info.IsGroup,
		At:      event.Info.Timestamp,
	}
	select {
	case a.incoming <- message:
	default:
		a.log.Warn("incoming queue full, dropping message", "sender", sender)
	}
}

// lookupPN resolves a LID through the WhatsApp LID store (ADR 0013).
func (a *Adapter) lookupPN(lid types.JID) (types.JID, bool) {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil || client.Store == nil || client.Store.LIDs == nil {
		return types.JID{}, false
	}
	pn, err := client.Store.LIDs.GetPNForLID(context.Background(), lid)
	if err != nil || pn.IsEmpty() {
		return types.JID{}, false
	}
	return pn, true
}

func (a *Adapter) loop() {
	defer close(a.stopped)
	for {
		select {
		case <-a.stop:
			return
		case <-a.reconnect:
			a.reconnectOnce()
		}
	}
}

func (a *Adapter) reconnectOnce() {
	delay := a.backoff.Next()
	a.log.Debug("reconnecting", "delay", delay)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-a.stop:
		return
	case <-timer.C:
	}
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil {
		return
	}
	if err := client.Connect(); err != nil {
		a.log.Warn("reconnect failed", "error", err, "attempt", a.backoff.attempt)
		a.signalReconnect()
	}
}

func (a *Adapter) signalReconnect() {
	select {
	case a.reconnect <- struct{}{}:
	default:
	}
}

func (a *Adapter) stopReconnecting() {
	a.stopOnce.Do(func() { close(a.stop) })
}

func messageText(message *waE2E.Message) string {
	if message == nil {
		return ""
	}
	if text := message.GetConversation(); text != "" {
		return text
	}
	if extended := message.GetExtendedTextMessage(); extended != nil {
		return extended.GetText()
	}
	return ""
}
