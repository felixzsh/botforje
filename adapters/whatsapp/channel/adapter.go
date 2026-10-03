// Package channel is the live WhatsApp connection: the whatsmeow
// implementation of botflota.MessageChannel for one bot (ADR 0001, ADR 0004).
// It owns reconnection, dedup, contact resolution, presence, and on-demand
// history sync (ADR 0011, ADR 0035).
package channel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	walog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
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

// Options requires Bot, Scope, Devices, Accounts, and Pairings.
type Options struct {
	Scope    string
	Bot      string
	Devices  *whatsapp.Devices
	Accounts botflota.LinkedAccountStore
	Pairings botflota.PairingStore
	// Contacts, Chats, and Messages are all required to import the account-wide
	// history sync WhatsApp delivers after linking.
	Contacts botflota.ContactStore
	Chats    botflota.ChatStore
	Messages botflota.MessageStore
	// History enables the inert import of that sync (ADR 0035).
	History bool
	// AddressBook receives the imported book when ImportContacts is on.
	AddressBook botflota.AddressBookStore
	// ImportContacts is off by default: the book may hold people this account
	// never wrote to.
	ImportContacts bool
	// HTTPClient defaults to http.DefaultClient; it fetches remote media.
	HTTPClient *http.Client
	Settings   Settings
	Log        *slog.Logger
	// DepsLogLevel is the minimum level for forwarded whatsmeow logs; the zero
	// value is info.
	DepsLogLevel slog.Level
	Queue        int
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	Now          func() time.Time
	Rand         *rand.Rand
	// Sleep lets tests avoid real pacing waits.
	Sleep   func(ctx context.Context, d time.Duration) error
	OnState func(State)
}

// Adapter is the whatsmeow implementation of botflota.MessageChannel for one
// bot.
type Adapter struct {
	scope    string
	bot      string
	accounts botflota.LinkedAccountStore
	pairings botflota.PairingStore
	devices  *whatsapp.Devices
	settings Settings
	http     *http.Client
	upload   uploader
	log      *slog.Logger
	waLogger walog.Logger
	backoff  *Backoff
	now      func() time.Time
	rnd      *rand.Rand
	sleep    func(ctx context.Context, d time.Duration) error
	onState  func(State)

	incoming chan botflota.IncomingMessage
	// outbound carries live messages that left the account outside Botflota,
	// so the fleet can hand the chat to a human (ADR 0043).
	outbound chan botflota.OutboundMessage

	contacts        botflota.ContactStore
	chats           botflota.ChatStore
	messages        botflota.MessageStore
	importer        *ingest.HistoryImporter
	addressBook     *ingest.AddressBookImporter
	addressBookOnce sync.Once
	history         chan *events.HistorySync
	// historyDone closes when the import worker has drained and stopped.
	historyDone chan struct{}

	// syncMu guards the one-at-a-time on-demand backfill.
	syncMu  sync.Mutex
	syncing bool
	pending *syncRequest

	mu     sync.Mutex
	client *whatsmeow.Client
	state  State
	seen   *dedup
	last   map[string]lastInbound

	// sentMu guards sentIDs, which Send writes and the event handler reads.
	sentMu  sync.Mutex
	sentIDs *dedup

	reconnect chan struct{}
	stop      chan struct{}
	stopped   chan struct{}
	stopOnce  sync.Once
}

type lastInbound struct {
	id   types.MessageID
	chat types.JID
	from types.JID
}

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
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	waLogger := whatsapp.NewWaLog(log, opts.DepsLogLevel)
	a := &Adapter{
		scope:     opts.Scope,
		bot:       opts.Bot,
		accounts:  opts.Accounts,
		pairings:  opts.Pairings,
		devices:   opts.Devices,
		settings:  opts.Settings,
		http:      httpClient,
		log:       log,
		waLogger:  waLogger,
		backoff:   NewBackoff(opts.MinBackoff, opts.MaxBackoff, rnd),
		now:       now,
		rnd:       rnd,
		sleep:     opts.Sleep,
		onState:   opts.OnState,
		contacts:  opts.Contacts,
		chats:     opts.Chats,
		messages:  opts.Messages,
		incoming:  make(chan botflota.IncomingMessage, queue),
		outbound:  make(chan botflota.OutboundMessage, queue),
		state:     StateStopped,
		seen:      newDedup(2048),
		sentIDs:   newDedup(2048),
		last:      map[string]lastInbound{},
		reconnect: make(chan struct{}, 1),
		stop:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
	if opts.ImportContacts {
		a.addressBook = ingest.NewAddressBook(
			a.scope, a.bot, opts.Contacts, opts.AddressBook, a.lookupPN, log)
	}
	if opts.History {
		a.importer = ingest.NewHistory(a.scope, a.bot, opts.Contacts, opts.Chats,
			opts.Messages, a.lookupPN, log)
		if a.importer != nil {
			a.history = make(chan *events.HistorySync, 16)
			a.historyDone = make(chan struct{})
			go a.importHistory()
		}
	}
	go a.loop()
	return a, nil
}

func (a *Adapter) Connect(ctx context.Context) error {
	account, err := a.accounts.Get(ctx, a.scope, a.bot)
	if err != nil {
		return fmt.Errorf("whatsapp: bot %q is not paired (use bot pair): %w", a.bot, err)
	}
	device, err := a.devices.Load(ctx, account)
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
	// A full address-book sync is otherwise applied silently, which would skip
	// every contact event this import runs on (ADR 0037).
	client.EmitAppStateEventsOnFullSync = a.addressBook != nil
	client.AddEventHandler(a.handleEvent)
	if a.importer != nil {
		a.importer.SetOwnPhone(whatsapp.DevicePhone(device))
	}

	a.mu.Lock()
	a.client = client
	a.mu.Unlock()
	a.setState(StateConnecting)

	if err := client.Connect(); err != nil {
		return fmt.Errorf("whatsapp: connect: %w", err)
	}
	return nil
}

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
	if a.historyDone != nil {
		<-a.historyDone
	}
	a.setState(StateStopped)
	return nil
}

func (a *Adapter) Incoming() <-chan botflota.IncomingMessage { return a.incoming }

func (a *Adapter) Outbound() <-chan botflota.OutboundMessage { return a.outbound }

// markSent remembers an id this adapter sent, so its echo is not mistaken for
// an operator reply (ADR 0043).
func (a *Adapter) markSent(id string) {
	if id == "" {
		return
	}
	a.sentMu.Lock()
	a.sentIDs.fresh(id)
	a.sentMu.Unlock()
}

func (a *Adapter) sentByUs(id string) bool {
	if id == "" {
		return false
	}
	a.sentMu.Lock()
	defer a.sentMu.Unlock()
	return a.sentIDs.contains(id)
}

func (a *Adapter) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *Adapter) ChannelState() string { return string(a.State()) }

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
	return whatsapp.UpdatePairingStatus(context.Background(), a.pairings,
		a.scope, a.bot, state, whatsapp.PairingNow(a.now))
}

func (a *Adapter) logPairingStatusError(err error) {
	if a.log != nil {
		a.log.Error("persist pairing status", "error", err)
	}
}

func missingDeviceError(account *botflota.LinkedAccount) error {
	return fmt.Errorf(
		"whatsapp: bot %q (scope %q) has no local device credentials; "+
			"use bot pair",
		account.Bot, account.Scope,
	)
}
