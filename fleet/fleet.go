// Package fleet runs one actor per configured bot, each with its own channel,
// engine, filter, and outbox (ADR 0011, docs/architecture.md).
package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/engine"
	"github.com/felixzsh/botflota/spec"
)

type BotSpec struct {
	Scope    string
	Name     string
	Settings spec.BotSettings
}

// Store, Chats, Senders, Messages, Turn, and NewChannel are required.
type Options struct {
	Store      botflota.SessionStore
	Chats      botflota.ChatStore
	Senders    botflota.SenderStore
	Messages   botflota.MessageStore
	Turn       botflota.TurnStore
	NewChannel func(spec BotSpec) (botflota.MessageChannel, error)
	// Skip is for recoverable runtime conditions such as a missing local
	// pairing; a skipped bot never gets a channel.
	Skip           func(scope, bot string) bool
	HTTPClient     *http.Client
	Now            func() time.Time
	Rand           *rand.Rand
	DefaultTimeout time.Duration
	OnError        func(scope, bot string, err error)
	Log            *slog.Logger
}

type Fleet struct {
	// mu guards the bot registry, the run context, and the recorded errors;
	// a built *Bot is immutable afterwards.
	mu   sync.Mutex
	bots map[string]*Bot
	cfg  *config.Config
	opts Options
	// ctx is set by Run and cleared when it returns; a bot may start only
	// while it is set (ADR 0022).
	ctx  context.Context
	gen  int64 // bumped by ApplyConfig; bots compare against it
	errs []error
	wait sync.WaitGroup
}

type Bot struct {
	scope   string
	name    string
	channel botflota.MessageChannel
	engine  *engine.Engine
	outbox  *outbox
	filter  Filter
	onError func(error)
	log     *slog.Logger
	fleet   *Fleet
	// gen is the config generation engine and filter were built from; only
	// the run goroutine touches these three (ADR 0009).
	gen int64
}

// New starts nothing and connects nothing. A bot whose pairing is missing is
// skipped (ADR 0022) and can join later with StartBot.
func New(cfg *config.Config, opts Options) (*Fleet, error) {
	if cfg == nil {
		return nil, errors.New("fleet: nil config")
	}
	if opts.Store == nil {
		return nil, errors.New("fleet: no session store")
	}
	if opts.Chats == nil {
		return nil, errors.New("fleet: no chat store")
	}
	if opts.Senders == nil {
		return nil, errors.New("fleet: no sender store")
	}
	if opts.Messages == nil {
		return nil, errors.New("fleet: no message store")
	}
	if opts.Turn == nil {
		return nil, errors.New("fleet: no turn store")
	}
	if opts.NewChannel == nil {
		return nil, errors.New("fleet: no channel factory")
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if opts.Log == nil {
		opts.Log = botflota.DiscardLogger()
	}
	fleet := &Fleet{cfg: cfg, opts: opts, bots: make(map[string]*Bot)}
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
		scope := cfg.Scopes[scopeName]
		for _, botName := range config.SortedKeys(scope.Bots) {
			bot, ok := scope.Bots[botName]
			if !ok {
				continue
			}
			if _, ok := scope.Graphs[bot.Graph]; !ok {
				return nil, fmt.Errorf(
					"fleet: bot %q references unknown graph %q", botName, bot.Graph)
			}
			if opts.Skip != nil && opts.Skip(scopeName, botName) {
				continue
			}
			built, err := fleet.buildBot(scopeName, botName)
			if err != nil {
				return nil, err
			}
			fleet.bots[scopeName+"/"+botName] = built
		}
	}
	return fleet, nil
}

// buildBot constructs one configured bot's engine, channel, and outbox from
// the config the fleet was built with; it starts nothing.
func (f *Fleet) buildBot(scopeName, botName string) (*Bot, error) {
	cfg := f.cfg
	eng, filter, settings, err := f.buildRuntime(cfg, scopeName, botName)
	if err != nil {
		return nil, err
	}
	opts := f.opts
	seeds := opts.Rand
	botLog := opts.Log.With("scope", scopeName, "bot", botName)
	channel, err := opts.NewChannel(BotSpec{
		Scope: scopeName, Name: botName, Settings: settings,
	})
	if err != nil {
		return nil, fmt.Errorf("fleet: channel for %q: %w", botName, err)
	}
	queue := newOutbox(outboxConfig{
		Sender:       channel.Send,
		Delay:        time.Duration(settings.QueueDelay) * time.Millisecond,
		Jitter:       time.Duration(settings.Jitter) * time.Millisecond,
		MaxPerMinute: settings.MaxMsgPerMinute,
		Now:          opts.Now,
		Rand:         rand.New(rand.NewSource(seeds.Int63())),
		Log:          botLog,
		OnError: func(_ botflota.OutgoingMessage, err error) {
			if opts.OnError != nil {
				opts.OnError(scopeName, botName, fmt.Errorf("send: %w", err))
			}
		},
		OnSent: func(msg botflota.OutgoingMessage, providerID string) {
			if msg.StoredMessageID == 0 {
				return
			}
			if err := opts.Messages.SetProviderID(
				context.Background(), msg.StoredMessageID, providerID,
			); err != nil {
				botLog.Warn("record outbound provider id", "error", err)
			}
		},
	})
	return &Bot{
		scope: scopeName, name: botName, channel: channel, engine: eng, outbox: queue,
		filter: filter, fleet: f, gen: f.gen,
		log: botLog,
		onError: func(err error) {
			if opts.OnError != nil {
				opts.OnError(scopeName, botName, err)
			}
		},
	}, nil
}

// buildRuntime resolves one bot's engine and filter against cfg: both adopt a
// replaced config at the bot's next message, unlike the channel and outbox
// (ADR 0009).
func (f *Fleet) buildRuntime(
	cfg *config.Config, scopeName, botName string,
) (*engine.Engine, Filter, spec.BotSettings, error) {
	zero := spec.BotSettings{}
	scope, ok := cfg.Scopes[scopeName]
	if !ok {
		return nil, Filter{}, zero, fmt.Errorf("fleet: unknown scope %q", scopeName)
	}
	bot, ok := scope.Bots[botName]
	if !ok {
		return nil, Filter{}, zero,
			fmt.Errorf("fleet: unknown bot %s/%s", scopeName, botName)
	}
	graph, ok := scope.Graphs[bot.Graph]
	if !ok {
		return nil, Filter{}, zero, fmt.Errorf(
			"fleet: bot %q references unknown graph %q", botName, bot.Graph)
	}
	opts := f.opts
	settings := settingsOf(bot.Settings)
	botLog := opts.Log.With("scope", scopeName, "bot", botName)
	eng := engine.New(engine.Options{
		Bot: botName, Scope: scopeName, Graph: graph, Actions: scope.Actions,
		Store: opts.Store, Chats: opts.Chats, Senders: opts.Senders,
		Messages:   opts.Messages,
		Turn:       opts.Turn,
		HTTPClient: opts.HTTPClient, Now: opts.Now,
		Rand:           rand.New(rand.NewSource(opts.Rand.Int63())),
		DefaultTimeout: opts.DefaultTimeout,
		Log:            botLog,
	})
	filter := Filter{
		IgnoreGroups: ignoreGroups(settings),
		Allowed:      settings.AllowedSenders,
		Ignored:      settings.IgnoredSenders,
	}
	return eng, filter, settings, nil
}

// Run starts every built bot and waits: it returns when ctx is cancelled or
// every bot has stopped, with the first bot error if any.
func (f *Fleet) Run(ctx context.Context) error {
	f.mu.Lock()
	f.ctx = ctx
	f.mu.Unlock()
	for _, bot := range f.ordered() {
		f.start(ctx, bot)
	}
	f.wait.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctx = nil
	if len(f.errs) > 0 {
		return f.errs[0]
	}
	return nil
}

// Start builds and runs one configured bot now: how a bot paired through the
// running instance joins the fleet without a restart (ADR 0022). A bot that
// is already running is left alone, and a fleet that is not running refuses.
//
// ponytail: start-during-shutdown races with Run's final wait; the pairing
// session finishing exactly as the fleet exits would be the window. If that
// ever bites, gate StartBot on a done channel closed before wait.Wait returns.
func (f *Fleet) StartBot(scope, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctx == nil || f.ctx.Err() != nil {
		return errors.New("fleet: not running")
	}
	key := scope + "/" + name
	if _, ok := f.bots[key]; ok {
		return nil
	}
	bot, err := f.buildBot(scope, name)
	if err != nil {
		return err
	}
	f.bots[key] = bot
	f.start(f.ctx, bot)
	return nil
}

// ApplyConfig adopts a validated configuration; each bot rebuilds its engine
// and filter at its next message, which is where a bot can adopt a change
// (ADR 0009).
func (f *Fleet) ApplyConfig(cfg *config.Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg = cfg
	f.gen++
}

// refresh rebuilds one bot's engine and filter when a new config was applied.
// Only the bot's run goroutine touches them afterwards, so no lock guards
// those fields.
func (f *Fleet) refresh(bot *Bot) error {
	f.mu.Lock()
	if bot.gen == f.gen {
		f.mu.Unlock()
		return nil
	}
	cfg, gen := f.cfg, f.gen
	f.mu.Unlock()

	eng, filter, _, err := f.buildRuntime(cfg, bot.scope, bot.name)
	if err != nil {
		return err
	}
	bot.engine, bot.filter, bot.gen = eng, filter, gen
	return nil
}

func (f *Fleet) start(ctx context.Context, bot *Bot) {
	f.wait.Add(1)
	go func() {
		defer f.wait.Done()
		if err := bot.run(ctx); err != nil {
			f.mu.Lock()
			f.errs = append(f.errs, fmt.Errorf("%s/%s: %w", bot.scope, bot.name, err))
			f.mu.Unlock()
		}
	}()
}

// ordered copies the built bots sorted by scope and name, so a caller walks
// them without holding the fleet lock.
func (f *Fleet) ordered() []*Bot {
	f.mu.Lock()
	bots := make([]*Bot, 0, len(f.bots))
	for _, bot := range f.bots {
		bots = append(bots, bot)
	}
	f.mu.Unlock()
	sort.Slice(bots, func(i, j int) bool {
		if bots[i].scope != bots[j].scope {
			return bots[i].scope < bots[j].scope
		}
		return bots[i].name < bots[j].name
	})
	return bots
}

func (f *Fleet) HistorySync(
	ctx context.Context, scope, bot, phone string,
	count int, timeout time.Duration,
) (botflota.HistoryResult, error) {
	for _, candidate := range f.ordered() {
		if candidate.scope != scope || candidate.name != bot {
			continue
		}
		syncer, ok := candidate.channel.(botflota.HistorySyncer)
		if !ok {
			return botflota.HistoryResult{}, fmt.Errorf(
				"fleet: bot %s/%s does not support on-demand history", scope, bot)
		}
		return syncer.SyncHistory(ctx, phone, count, timeout)
	}
	return botflota.HistoryResult{}, fmt.Errorf("fleet: unknown bot %s/%s", scope, bot)
}

func (f *Fleet) Status() []botflota.BotStatus {
	bots := f.ordered()
	statuses := make([]botflota.BotStatus, 0, len(bots))
	for _, bot := range bots {
		status := botflota.BotStatus{
			Scope: bot.scope, Bot: bot.name, OutboxDepth: bot.outbox.Depth(),
		}
		if reporter, ok := bot.channel.(botflota.StateReporter); ok {
			status.State = reporter.ChannelState()
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (b *Bot) run(ctx context.Context) error {
	if err := b.channel.Connect(ctx); err != nil {
		b.log.Error("channel connect failed", "error", err)
		return err
	}
	b.log.Info("bot started")
	defer func() {
		// Stop accepting, let queued messages go out, then disconnect.
		b.outbox.Close()
		b.outbox.Wait()
		if err := b.channel.Disconnect(context.Background()); err != nil {
			b.log.Error("channel disconnect failed", "error", err)
		}
		b.log.Info("bot stopped")
	}()
	incoming := b.channel.Incoming()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-incoming:
			if !ok {
				return nil
			}
			b.log.Debug("message received",
				"sender", msg.SenderPhone, "id", msg.ProviderMessageID,
				"group", msg.IsGroup,
				"kind", string(botflota.ContentKind(msg.Content)),
				"body", botflota.ContentText(msg.Content))
			if err := b.fleet.refresh(b); err != nil {
				b.log.Warn("apply config change", "error", err)
			}
			if !b.filter.Allow(msg) {
				b.log.Debug("message filtered", "sender", msg.SenderPhone)
				continue
			}
			replies, err := b.engine.Handle(ctx, msg)
			if err != nil {
				b.log.Error("engine failed", "sender", msg.SenderPhone, "error", err)
				b.onError(err)
				continue
			}
			for _, reply := range replies {
				if err := b.outbox.Push(ctx, reply); err != nil {
					b.log.Debug("enqueue reply failed", "error", err)
					return err
				}
			}
		}
	}
}

func settingsOf(settings *spec.BotSettings) spec.BotSettings {
	if settings == nil {
		return spec.BotSettings{}
	}
	return *settings
}

func ignoreGroups(settings spec.BotSettings) bool {
	if settings.IgnoreGroups == nil {
		return true
	}
	return *settings.IgnoreGroups
}
