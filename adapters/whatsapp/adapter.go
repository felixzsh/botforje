// Package whatsapp implements the MessageChannel port with whatsmeow
// (ADR 0001, ADR 0004). It owns the device store, reconnection, dedup, sender
// resolution, and presence (ADR 0011, ADR 0013).
package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	walog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botflota"
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
	ReadReceipts     bool
	ReadReceiptDelay time.Duration
	ReplyDelay       time.Duration
	SimulateTyping   bool
	// Jitter is added once per delay as a random value in [0, Jitter].
	Jitter time.Duration
}

// Options configures an Adapter. Bot, Scope, Devices, Accounts, and Pairings
// are required.
type Options struct {
	Scope    string
	Bot      string
	Devices  *Devices
	Accounts botflota.LinkedAccountStore
	Pairings botflota.PairingStore
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
	// Sleep waits for a pacing delay; tests inject a fake to avoid real waits.
	Sleep func(ctx context.Context, d time.Duration) error
	// OnState is called when the channel lifecycle changes, for status.
	OnState func(State)
}

// Adapter is the whatsmeow implementation of botflota.MessageChannel for one
// bot. Create it with New, then Connect.
type Adapter struct {
	scope    string
	bot      string
	accounts botflota.LinkedAccountStore
	pairings botflota.PairingStore
	devices  *Devices
	settings Settings
	log      *slog.Logger
	waLogger walog.Logger
	backoff  *Backoff
	now      func() time.Time
	rnd      *rand.Rand
	sleep    func(ctx context.Context, d time.Duration) error
	onState  func(State)

	incoming chan botflota.IncomingMessage

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

// New builds the adapter on the shared device store.
func New(opts Options) (*Adapter, error) {
	if opts.Devices == nil {
		return nil, errors.New("whatsapp: no device store")
	}
	var (
		log   = opts.Log
		now   = opts.Now
		rnd   = opts.Rand
		queue = opts.Queue
	)
	if log == nil {
		log = botflota.DiscardLogger()
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
		pairings:  opts.Pairings,
		devices:   opts.Devices,
		settings:  opts.Settings,
		log:       log,
		waLogger:  waLogger,
		backoff:   NewBackoff(opts.MinBackoff, opts.MaxBackoff, rnd),
		now:       now,
		rnd:       rnd,
		sleep:     opts.Sleep,
		onState:   opts.OnState,
		incoming:  make(chan botflota.IncomingMessage, queue),
		state:     StateStopped,
		seen:      newDedup(2048),
		last:      map[string]lastInbound{},
		reconnect: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	go a.loop()
	return a, nil
}

// Connect loads the paired device and starts the client.
func (a *Adapter) Connect(ctx context.Context) error {
	account, err := a.accounts.Get(ctx, a.scope, a.bot)
	if err != nil {
		return fmt.Errorf("whatsapp: bot %q is not paired (use bot pair): %w", a.bot, err)
	}
	device, err := a.devices.device(ctx, account)
	if err != nil {
		return err
	}
	if device == nil {
		if err := a.updatePairingStatus(botflota.PairingUnpaired); err != nil {
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

// Send delivers one message with human-like pacing (ADR 0011): it waits
// read_receipt_delay before marking the inbound read, waits reply_delay while
// showing composing presence, then sends. Jitter is drawn once per delay. The
// recipient is a plain phone number; the adapter maps it to the JID the message
// arrived from, or to the phone-number JID when that is unknown (ADR 0020).
func (a *Adapter) Send(ctx context.Context, msg botflota.OutgoingMessage) error {
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
	readDelay := a.jittered(a.settings.ReadReceiptDelay)
	replyDelay := a.jittered(a.settings.ReplyDelay)
	if a.settings.ReadReceipts && !last.chat.IsEmpty() {
		if err := a.sleepFor(ctx, readDelay); err != nil {
			return err
		}
		if err := client.MarkRead(ctx, []types.MessageID{last.id}, a.now(),
			last.chat, last.sender); err != nil {
			a.log.Warn("read receipt failed", "error", err)
		}
	}
	content, err := buildContent(msg)
	if err != nil {
		return err
	}
	a.log.Debug("reply pacing", "read_receipt_delay", readDelay,
		"reply_delay", replyDelay, "jitter", a.settings.Jitter)
	composing := false
	if a.settings.SimulateTyping {
		if err := client.SendChatPresence(ctx, to, types.ChatPresenceComposing,
			types.ChatPresenceMediaText); err != nil {
			a.log.Warn("composing presence failed", "error", err)
		} else {
			composing = true
		}
	}
	if err := a.sleepFor(ctx, replyDelay); err != nil {
		return err
	}
	_, err = client.SendMessage(ctx, to, content)
	if composing {
		_ = client.SendChatPresence(ctx, to, types.ChatPresencePaused,
			types.ChatPresenceMediaText)
	}
	return err
}

// Incoming is the stream of mapped inbound messages.
func (a *Adapter) Incoming() <-chan botflota.IncomingMessage { return a.incoming }

// State reports the current lifecycle state.
func (a *Adapter) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// ChannelState reports the lifecycle state as a string (botflota.StateReporter).
func (a *Adapter) ChannelState() string { return string(a.State()) }

// jittered adds a random value in [0, jitter] to one delay. Each call draws
// again, so consecutive replies never share the same pacing.
func (a *Adapter) jittered(delay time.Duration) time.Duration {
	if a.settings.Jitter <= 0 || a.rnd == nil {
		return delay
	}
	return delay + time.Duration(a.rnd.Int63n(int64(a.settings.Jitter)+1))
}

// sleepFor waits for one pacing delay and honors cancellation. Tests inject a
// fake sleep so they never wait in real time.
func (a *Adapter) sleepFor(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if a.sleep != nil {
		return a.sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
		if err := a.updatePairingStatus(botflota.PairingPaired); err != nil {
			a.logPairingStatusError(err)
		}
	} else if state == StateLoggedOut {
		if err := a.updatePairingStatus(botflota.PairingUnpaired); err != nil {
			a.logPairingStatusError(err)
		}
	}
	if a.onState != nil {
		a.onState(state)
	}
}

func (a *Adapter) updatePairingStatus(state botflota.PairingState) error {
	if a.pairings == nil {
		return nil
	}
	return updatePairingStatus(context.Background(), a.pairings, a.scope, a.bot,
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

	message := botflota.IncomingMessage{
		Bot:        a.bot,
		Sender:     sender,
		SenderName: event.Info.PushName,
		Body:       body,
		ID:         id,
		IsGroup:    event.Info.IsGroup,
		At:         event.Info.Timestamp,
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
