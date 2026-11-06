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
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/engine"
	"github.com/felixzsh/botflota/spec"
)

// Store, Chats, Peers, Messages, Turn, Outbox, and Catalog are required.
type Options struct {
	Store    botflota.SessionStore
	Chats    botflota.ChatStore
	Peers    botflota.PeerStore
	Messages botflota.MessageStore
	Turn     botflota.TurnStore
	Outbox   botflota.OutboxStore
	// Bindings rechecks the committed generation before every send; nil
	// skips the durable enforcement gate (ADR 0040). Production always sets
	// it; tests that drive the worker without bindings leave it unset.
	Bindings botflota.BindingStore
	// Notify receives each bot's first-contact hook; nil means no consumer
	// (ADR 0045).
	Notify botflota.PeerFirstSeenNotifier
	// Catalog holds the installed channel definitions; the fleet resolves each
	// bot's selector against it and asks the adapter's own builder for the
	// channel (ADR 0052, ADR 0054).
	Catalog *channels.Catalog
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
	mu      sync.Mutex
	applyMu sync.Mutex
	bots    map[botflota.BotRef]*Bot
	cfg     *config.Config
	opts    Options
	// ctx is set by Run and cleared when it returns; a bot may start only
	// while it is set (ADR 0022).
	ctx      context.Context
	scopeGen map[string]int64
	errs     []error
	wait     sync.WaitGroup
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
	// stop asks the run goroutine to exit; done closes once it has drained
	// the outbox and disconnected, which is the moment its credentials may
	// be replaced (ADR 0011, ADR 0021). running is what the lifecycle
	// methods read, stored and cleared by the goroutine itself.
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	running  atomic.Bool
	// started records that this bot has had a run goroutine, under
	// Fleet.mu: StopBot waits on done for one, while a bot built but never
	// started has no done to wait for.
	started   bool
	runtimeMu sync.Mutex

	// externalPause is the bot's hand-over window; the run goroutine reads it
	// and refresh updates it with the engine (ADR 0043).
	externalPause time.Duration
	now           func() time.Time
	// controlMu guards control, the per-chat hand-over state the run goroutine
	// and the API both touch (ADR 0043).
	controlMu sync.Mutex
	control   map[botflota.ChatTarget]chatControl
}

// defaultExternalReplyPause is the hand-over window when a bot does not set
// one (ADR 0043).
const defaultExternalReplyPause = 15 * time.Minute

// chatControl is one chat's live hand-over state: a countdown started by an
// external reply, or an indefinite manual hold (ADR 0043).
type chatControl struct {
	human      bool
	pausedTill time.Time
}

// externalReplyPause resolves one bot's hand-over window; a zero or negative
// setting uses the default.
func externalReplyPause(settings spec.BotSettings) time.Duration {
	if settings.ExternalReplyPause <= 0 {
		return defaultExternalReplyPause
	}
	return time.Duration(settings.ExternalReplyPause) * time.Millisecond
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
	if opts.Peers == nil {
		return nil, errors.New("fleet: no peer store")
	}
	if opts.Messages == nil {
		return nil, errors.New("fleet: no message store")
	}
	if opts.Turn == nil {
		return nil, errors.New("fleet: no turn store")
	}
	if opts.Outbox == nil {
		return nil, errors.New("fleet: no outbox store")
	}
	if opts.Catalog == nil {
		return nil, errors.New("fleet: no channel catalog")
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if opts.Log == nil {
		opts.Log = botflota.DiscardLogger()
	}
	fleet := &Fleet{
		cfg: cfg, opts: opts, bots: make(map[botflota.BotRef]*Bot),
		scopeGen: make(map[string]int64),
	}
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
			ready, state, err := fleet.prepareBot(context.Background(), scopeName, botName)
			if err != nil {
				return nil, err
			}
			if !ready {
				opts.Log.Warn("bot not ready; skipping it",
					"scope", scopeName, "bot", botName, "state", state)
				continue
			}
			invalid, err := fleet.hasInvalidActiveSession(
				context.Background(), scopeName, botName, scope.Graphs[bot.Graph])
			if err != nil {
				return nil, fmt.Errorf(
					"fleet: inspect sessions for %s/%s: %w", scopeName, botName, err)
			}
			if invalid {
				opts.Log.Warn("bot not initialized; an active session references a removed node",
					"scope", scopeName, "bot", botName)
				continue
			}
			built, err := fleet.buildBot(scopeName, botName)
			if err != nil {
				return nil, err
			}
			fleet.bots[botflota.BotRef{Scope: scopeName, Bot: botName}] = built
		}
	}
	return fleet, nil
}

// buildBot constructs one configured bot's engine, channel, and outbox from
// the config the fleet was built with; it does not connect the channel or
// start the outbox worker.
func (f *Fleet) buildBot(scopeName, botName string) (*Bot, error) {
	cfg := f.cfg
	opts := f.opts
	seeds := opts.Rand
	botLog := opts.Log.With("scope", scopeName, "bot", botName)
	scope, ok := cfg.Scopes[scopeName]
	if !ok {
		return nil, fmt.Errorf("fleet: unknown scope %q", scopeName)
	}
	bot, ok := scope.Bots[botName]
	if !ok {
		return nil, fmt.Errorf("fleet: unknown bot %s/%s", scopeName, botName)
	}
	def, _, err := opts.Catalog.Resolve(bot.Channel)
	if err != nil {
		return nil, fmt.Errorf("fleet: bot %q: %w", botName, err)
	}
	eng, filter, settings, err := f.buildRuntime(cfg, scopeName, botName)
	if err != nil {
		return nil, err
	}
	options, reasons := decodeOptions(def, bot)
	if len(reasons) > 0 {
		return nil, fmt.Errorf("fleet: bot %q channel: %s", botName, strings.Join(reasons, "; "))
	}
	if def.Build == nil {
		return nil, fmt.Errorf(
			"fleet: channel %s/%s has no builder in this installation",
			def.Provider, def.Kind)
	}
	channel, err := def.Build(channels.Bind{
		Scope: scopeName, Bot: botName, Options: options,
		FileRoot: cfg.FileRoots[scopeName], Log: botLog,
	})
	if err != nil {
		return nil, fmt.Errorf("fleet: channel for %q: %w", botName, err)
	}
	queue := newOutbox(outboxConfig{
		Store:        opts.Outbox,
		Scope:        scopeName,
		Bot:          botName,
		Send:         channel.Send,
		Delay:        time.Duration(settings.QueueDelay) * time.Millisecond,
		Jitter:       time.Duration(settings.Jitter) * time.Millisecond,
		MaxPerMinute: settings.MaxMsgPerMinute,
		Now:          opts.Now,
		Rand:         rand.New(rand.NewSource(seeds.Int63())),
		Log:          botLog,
		Bindings:     opts.Bindings,
		// Delivery re-invokes the installed definition's admission
		// independently of queue admission: an eligibility change since
		// then fails the entry permanently, without downgrade (ADR 0052).
		CheckDelivery: deliveryCheck(opts.Catalog, bot.Channel),
		// Lost authorization stops the bot with its queue pending and
		// visible; a replacement's FailPending still owns the old rows.
		// The stop runs off the worker goroutine: the run loop's
		// shutdown waits for this worker, so a synchronous stop would
		// deadlock (ADR 0040).
		OnAuthLost: func(entry botflota.OutboxEntry) {
			botLog.Warn("binding authorization lost; stopping with queue pending",
				"outbox_id", entry.ID)
			go func() {
				if err := f.stopBot(scopeName, botName, true); err != nil {
					botLog.Error("stop bot after authorization loss", "error", err)
				}
			}()
		},
		OnError: func(_ botflota.OutgoingMessage, err error) {
			if opts.OnError != nil {
				opts.OnError(scopeName, botName, fmt.Errorf("send: %w", err))
			}
		},
	})
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Bot{
		scope: scopeName, name: botName, channel: channel, engine: eng, outbox: queue,
		filter: filter, fleet: f, gen: f.scopeGen[scopeName],
		externalPause: externalReplyPause(settings), now: now,
		control: map[botflota.ChatTarget]chatControl{},
		stop:    make(chan struct{}), done: make(chan struct{}),
		log: botLog,
		onError: func(err error) {
			if opts.OnError != nil {
				opts.OnError(scopeName, botName, err)
			}
		},
	}, nil
}

// prepareBot asks the bot's definition whether it can join the fleet now. A
// definition without a Prepare hook is always ready (ADR 0022, ADR 0053).
func (f *Fleet) prepareBot(
	ctx context.Context, scopeName, botName string,
) (bool, string, error) {
	scope, ok := f.cfg.Scopes[scopeName]
	if !ok {
		return false, "", fmt.Errorf("fleet: unknown scope %q", scopeName)
	}
	bot, ok := scope.Bots[botName]
	if !ok {
		return false, "", fmt.Errorf("fleet: unknown bot %s/%s", scopeName, botName)
	}
	def, _, err := f.opts.Catalog.Resolve(bot.Channel)
	if err != nil {
		return false, "", fmt.Errorf("fleet: bot %q: %w", botName, err)
	}
	if def.Prepare == nil {
		return true, "", nil
	}
	options, reasons := decodeOptions(def, bot)
	if len(reasons) > 0 {
		return false, "", fmt.Errorf(
			"fleet: bot %q channel: %s", botName, strings.Join(reasons, "; "))
	}
	return def.Prepare(ctx, channels.Bind{
		Scope: scopeName, Bot: botName, Options: options, Log: f.opts.Log,
	})
}

// SetupFor resolves one bot's neutral account lifecycle and setup runner
// through its definition: inspection, bind, and unbind select the bot's
// installed implementation, never every bot through one device store
// (ADR 0053). A definition without a Setup hook offers no account setup.
func (f *Fleet) SetupFor(
	scopeName, botName string,
) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
	scope, ok := f.cfg.Scopes[scopeName]
	if !ok {
		return nil, nil, fmt.Errorf("fleet: unknown scope %q", scopeName)
	}
	bot, ok := scope.Bots[botName]
	if !ok {
		return nil, nil, fmt.Errorf("fleet: unknown bot %s/%s", scopeName, botName)
	}
	def, _, err := f.opts.Catalog.Resolve(bot.Channel)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet: bot %q: %w", botName, err)
	}
	if def.Setup == nil {
		return nil, nil, fmt.Errorf(
			"fleet: bot %q channel %s/%s offers no account setup: %w",
			botName, bot.Channel.Provider, bot.Channel.Mode, botflota.ErrNoSetup)
	}
	options, reasons := decodeOptions(def, bot)
	if len(reasons) > 0 {
		return nil, nil, fmt.Errorf(
			"fleet: bot %q channel: %s", botName, strings.Join(reasons, "; "))
	}
	return def.Setup(channels.Bind{
		Scope: scopeName, Bot: botName, Options: options, Log: f.opts.Log,
	})
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
		Store: opts.Store, Chats: opts.Chats, Peers: opts.Peers,
		Messages:   opts.Messages,
		Turn:       opts.Turn,
		Admit:      admitReplies(opts.Catalog, bot.Channel),
		Space:      spaceOf(opts.Catalog, bot.Channel),
		Bindings:   opts.Bindings,
		Notify:     opts.Notify,
		HTTPClient: opts.HTTPClient, Now: opts.Now,
		Rand:           rand.New(rand.NewSource(opts.Rand.Int63())),
		DefaultTimeout: opts.DefaultTimeout,
		Log:            botLog,
	})
	filter := Filter{
		Provider: bot.Channel.Provider,
		Allowed:  settings.AllowedPeers,
		Ignored:  settings.IgnoredPeers,
	}
	return eng, filter, settings, nil
}

// Run starts every built bot and keeps the fleet alive until ctx is
// cancelled: the instance ends the fleet, not its bot set, so a fleet that
// runs no bot still accepts the first pairing and stopping the last bot
// never shuts the instance down (ADR 0022). Publishing the run context and
// starting the bots happen under one lock, so a StartBot racing startup
// never produces two runs of one bot; the context is cleared under that
// same lock before the final wait, so no bot joins once shutdown began.
// The first bot error, if any, is returned at the end.
func (f *Fleet) Run(ctx context.Context) error {
	bots := f.ordered()
	f.mu.Lock()
	f.ctx = ctx
	for _, bot := range bots {
		f.startLocked(ctx, bot)
	}
	f.mu.Unlock()

	<-ctx.Done()

	f.mu.Lock()
	f.ctx = nil
	f.mu.Unlock()
	f.wait.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		return f.errs[0]
	}
	return nil
}

// StartBot builds and runs one configured bot now: how a bot paired through
// the running instance joins the fleet without a restart (ADR 0022). A bot
// that is already running is left alone; one that stopped — or whose channel
// failed — gets a fresh build, because after a re-pair the device store it
// was built with no longer exists (ADR 0021). A fleet that is not running
// refuses, and so does one whose context is already cancelled: Run clears it
// under this lock before its final wait, so a pairing finishing as the
// instance exits never joins a fleet that is shutting down.
func (f *Fleet) StartBot(scope, name string) error {
	f.applyMu.Lock()
	defer f.applyMu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ctx == nil || f.ctx.Err() != nil {
		return errors.New("fleet: not running")
	}
	botSpec, ok := f.cfg.Scopes[scope].Bots[name]
	if !ok {
		return fmt.Errorf("fleet: unknown bot %s/%s", scope, name)
	}
	if invalid, err := f.hasInvalidActiveSession(
		f.ctx, scope, name, f.cfg.Scopes[scope].Graphs[botSpec.Graph]); err != nil {
		return err
	} else if invalid {
		return fmt.Errorf("fleet: active session for %s/%s references a removed node",
			scope, name)
	}
	ready, state, err := f.prepareBot(f.ctx, scope, name)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("fleet: bot %s/%s is not ready (%s)", scope, name, state)
	}
	key := botflota.BotRef{Scope: scope, Bot: name}
	if bot, ok := f.bots[key]; ok && bot.running.Load() {
		return nil
	}
	bot, err := f.buildBot(scope, name)
	if err != nil {
		return err
	}
	f.bots[key] = bot
	f.startLocked(f.ctx, bot)
	return nil
}

func (f *Fleet) hasInvalidActiveSession(
	ctx context.Context, scope, bot string, graph spec.Graph,
) (bool, error) {
	return f.opts.Store.HasActiveSessionOutsideNodes(
		ctx, scope, bot, config.SortedKeys(graph.Nodes), f.now())
}

func (f *Fleet) now() time.Time {
	if f.opts.Now != nil {
		return f.opts.Now()
	}
	return time.Now()
}

// StopBot stops one bot and waits for its full shutdown: the outbox drains,
// the channel disconnects, and only then does the caller get a stopped bot
// whose credentials may be removed — one account keeps one connection (ADR
// 0011, ADR 0021). It waits for the run goroutine itself, not for the
// running flag the goroutine clears on its way out, so a bot that began
// stopping is never reported finished early. A bot that never started has
// no goroutine to wait for and answers nil, so the operation is idempotent.
func (f *Fleet) StopBot(scope, name string) error {
	return f.stopBot(scope, name, false)
}

// StopBotForUnpair lets an in-flight send finish but does not drain later
// replies; the account removal marks those replies failed (ADR 0040).
func (f *Fleet) StopBotForUnpair(scope, name string) error {
	return f.stopBot(scope, name, true)
}

func (f *Fleet) stopBot(scope, name string, discard bool) error {
	f.mu.Lock()
	bot, ok := f.bots[botflota.BotRef{Scope: scope, Bot: name}]
	if !ok || !bot.started {
		f.mu.Unlock()
		return nil
	}
	if discard {
		bot.outbox.Discard()
	}
	bot.stopOnce.Do(func() { close(bot.stop) })
	done := bot.done
	f.mu.Unlock()
	<-done
	return nil
}

// ApplyConfig adopts only safe changed scopes. A topology change waits until
// every affected bot has no live session; other scopes remain untouched.
func (f *Fleet) ApplyConfig(
	ctx context.Context, cfg *config.Config, scopes []string, source *config.Source,
) ([]string, map[string][]string, error) {
	f.applyMu.Lock()
	defer f.applyMu.Unlock()
	f.mu.Lock()
	plan := config.PlanReload(f.cfg, cfg)
	actors := make([]*Bot, 0)
	for _, bot := range f.orderedLocked() {
		if slices.Contains(scopes, bot.scope) &&
			len(plan.ScopeReasons[bot.scope]) == 0 {
			actors = append(actors, bot)
		}
	}
	f.mu.Unlock()
	for _, bot := range actors {
		bot.runtimeMu.Lock()
	}
	defer func() {
		for i := len(actors) - 1; i >= 0; i-- {
			actors[i].runtimeMu.Unlock()
		}
	}()

	f.mu.Lock()
	defer f.mu.Unlock()
	applied := make([]string, 0, len(scopes))
	pending := make(map[string][]string)
	plan = config.PlanReload(f.cfg, cfg)
	for _, scope := range scopes {
		was, existed := f.cfg.Scopes[scope]
		now, exists := cfg.Scopes[scope]
		if !existed || !exists {
			pending[scope] = []string{"scope set changed"}
			continue
		}
		reasons := plan.ScopeReasons[scope]
		if len(reasons) > 0 {
			pending[scope] = reasons
			continue
		}
		for _, bot := range config.SortedKeys(was.Bots) {
			oldBot, nextBot := was.Bots[bot], now.Bots[bot]
			if oldBot.Graph == nextBot.Graph && !graphTopologyChanged(
				was.Graphs[oldBot.Graph], now.Graphs[nextBot.Graph]) {
				continue
			}
			count, err := f.opts.Store.CountActive(ctx, scope, bot, f.now())
			if err != nil {
				return nil, nil, fmt.Errorf(
					"fleet: count active sessions for %s/%s: %w", scope, bot, err)
			}
			if count > 0 {
				reasons = append(reasons,
					fmt.Sprintf("graph topology of bot %q has active sessions", bot))
			}
		}
		if len(reasons) > 0 {
			pending[scope] = reasons
			continue
		}
		applied = append(applied, scope)
	}
	if len(applied) > 0 {
		f.cfg = config.WithScopes(f.cfg, cfg, applied)
		for _, scope := range applied {
			f.scopeGen[scope]++
		}
	}
	if source != nil {
		source.Replace(config.WithScopes(source.Current(), cfg, applied))
	}
	return applied, pending, nil
}

func (f *Fleet) orderedLocked() []*Bot {
	bots := make([]*Bot, 0, len(f.bots))
	for _, bot := range f.bots {
		bots = append(bots, bot)
	}
	sort.Slice(bots, func(i, j int) bool {
		if bots[i].scope != bots[j].scope {
			return bots[i].scope < bots[j].scope
		}
		return bots[i].name < bots[j].name
	})
	return bots
}

// refresh rebuilds one bot's engine and filter when a new config was applied.
// Only the bot's run goroutine touches them afterwards, so no lock guards
// those fields.
func (f *Fleet) refresh(bot *Bot) error {
	f.mu.Lock()
	if bot.gen == f.scopeGen[bot.scope] {
		f.mu.Unlock()
		return nil
	}
	cfg, gen := f.cfg, f.scopeGen[bot.scope]
	f.mu.Unlock()

	eng, filter, settings, err := f.buildRuntime(cfg, bot.scope, bot.name)
	if err != nil {
		return err
	}
	bot.engine, bot.filter, bot.gen = eng, filter, gen
	bot.externalPause = externalReplyPause(settings)
	return nil
}

func graphTopologyChanged(was, now spec.Graph) bool {
	if was.Root != now.Root || was.Fallback != now.Fallback ||
		!sameKeys(was.Nodes, now.Nodes) ||
		!reflect.DeepEqual(was.InitialContext, now.InitialContext) {
		return true
	}
	for name, oldNode := range was.Nodes {
		next := now.Nodes[name]
		if !reflect.DeepEqual(oldNode.InitialContext, next.InitialContext) ||
			!reflect.DeepEqual(oldNode.Edges, next.Edges) {
			return true
		}
	}
	return false
}

func sameKeys[V any](was, now map[string]V) bool {
	if len(was) != len(now) {
		return false
	}
	for key := range was {
		if _, ok := now[key]; !ok {
			return false
		}
	}
	return true
}

// startLocked launches one bot's run goroutine; the caller holds f.mu.
func (f *Fleet) startLocked(ctx context.Context, bot *Bot) {
	bot.started = true
	bot.running.Store(true)
	f.wait.Add(1)
	go func() {
		defer f.wait.Done()
		defer close(bot.done)
		err := bot.run(ctx)
		f.mu.Lock()
		bot.running.Store(false)
		if err != nil {
			f.errs = append(f.errs, fmt.Errorf("%s/%s: %w", bot.scope, bot.name, err))
		}
		f.mu.Unlock()
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
	ctx context.Context, scope, bot string, target botflota.ChatTarget,
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
		return syncer.SyncHistory(ctx, target, count, timeout)
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
		if !bot.running.Load() {
			// Never started, stopped on purpose, or its channel failed: the
			// channel's own view would claim otherwise.
			status.State = "stopped"
		} else if reporter, ok := bot.channel.(botflota.StateReporter); ok {
			status.State = reporter.ChannelState()
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (b *Bot) run(ctx context.Context) error {
	defer func() {
		// Cleanup also runs when the initial channel connection fails.
		b.outbox.Close()
		b.outbox.Wait()
		if err := b.channel.Disconnect(context.Background()); err != nil {
			b.log.Error("channel disconnect failed", "error", err)
		}
		b.log.Info("bot stopped")
	}()
	if err := b.channel.Connect(ctx); err != nil {
		b.log.Error("channel connect failed", "error", err)
		return err
	}
	b.log.Info("bot started")
	b.outbox.Start()
	incoming := b.channel.Incoming()
	var outbound <-chan botflota.ExternalSend
	if observer, ok := b.channel.(botflota.OutboundObserver); ok {
		outbound = observer.Outbound()
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-b.stop:
			return nil
		case msg, ok := <-incoming:
			if !ok {
				return nil
			}
			b.handleMessage(ctx, msg)
		case out, ok := <-outbound:
			if !ok {
				// A channel that closed its observations keeps the bot alive.
				outbound = nil
				continue
			}
			b.handleExternal(ctx, out)
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg botflota.IncomingMessage) {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	b.log.Debug("message received",
		"peer", msg.Sender.Subject, "id", msg.ProviderMessageID,
		"kind", string(botflota.ContentKind(msg.Content)),
		"body", botflota.ContentText(msg.Content))
	if err := b.fleet.refresh(b); err != nil {
		b.log.Warn("apply config change", "error", err)
	}
	if !b.filter.Allow(msg) {
		b.log.Debug("message filtered", "peer", msg.Sender.Subject)
		// A filtered message is a deliberate discard, never a failure:
		// it stores nothing, so there is no history to wait for.
		b.settleReceive(ctx, msg, botflota.ReceiveDiscarded)
		return
	}
	if b.paused(msg.Target, b.now()) {
		b.log.Info("chat held by a human; message stored without a reply",
			"peer", msg.Sender.Subject)
		outcome, err := b.engine.RecordSuppressed(ctx, msg)
		if err != nil {
			b.log.Error("record held message",
				"peer", msg.Sender.Subject, "error", err)
			b.onError(err)
			return
		}
		// A held inbound confirms only after its history is stored.
		b.settleReceive(ctx, msg, outcome)
		return
	}
	replies, outcome, err := b.engine.Handle(ctx, msg)
	if err != nil {
		b.log.Error("engine failed", "peer", msg.Sender.Subject, "error", err)
		b.onError(err)
		return
	}
	if len(replies) > 0 {
		b.outbox.Wake()
	}
	// The turn committed above; confirming does not wait for the queued
	// replies to leave the outbox. A failure never reaches this call, so
	// its entry stays replayable.
	b.settleReceive(ctx, msg, outcome)
}

// settleReceive confirms one delivered entry after its agreed processing
// outcome (ADR 0004, ADR 0040). It never confirms on handoff, never
// confirms a failure, and never calls a channel that cannot confirm: a
// source without the optional port, or an entry without a cursor, settles
// nothing. A failed confirmation is logged and reported; the processing
// already committed, so it is not retried here.
func (b *Bot) settleReceive(
	ctx context.Context, msg botflota.IncomingMessage, outcome botflota.ReceiveOutcome,
) {
	if outcome == botflota.ReceiveFailed || msg.ReceiveCursor == "" {
		return
	}
	confirmer, ok := b.channel.(botflota.ReceiveConfirmer)
	if !ok {
		return
	}
	receipt := botflota.ReceiveReceipt{Cursor: msg.ReceiveCursor, Outcome: outcome}
	if err := confirmer.ConfirmReceive(ctx, receipt); err != nil {
		b.log.Error("receive confirmation failed",
			"outcome", string(outcome), "error", err)
		b.onError(err)
	}
}

// handleExternal records a reply the operator sent from another client and
// hands its chat over for the bot's configured window (ADR 0043).
func (b *Bot) handleExternal(ctx context.Context, out botflota.ExternalSend) {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	// Cancel what the bot still owes the chat before pausing it, so no queued
	// reply leaves after the operator's own (ADR 0043).
	if err := b.fleet.cancelChat(ctx, b.scope, b.name, out.Target); err != nil {
		b.log.Warn("cancel queued replies for a hand-over",
			"peer", out.Target.Thread, "error", err)
	}
	b.noteExternal(out.Target, b.now())
	if err := b.engine.RecordOperator(ctx, out); err != nil {
		b.log.Warn("record operator reply", "peer", out.Target.Thread, "error", err)
	}
	b.log.Info("chat handed to a human",
		"peer", out.Target.Thread, "pause", b.externalPause)
}

// paused reports whether one chat is held right now: an indefinite manual hold
// or an automatic countdown still running. A finished countdown is forgotten,
// so the map does not grow with every chat that was ever paused (ADR 0043).
func (b *Bot) paused(target botflota.ChatTarget, now time.Time) bool {
	b.controlMu.Lock()
	defer b.controlMu.Unlock()
	state, ok := b.control[target]
	if !ok {
		return false
	}
	if state.human {
		return true
	}
	if now.Before(state.pausedTill) {
		return true
	}
	delete(b.control, target)
	return false
}

// noteExternal restarts one chat's hand-over countdown after an external reply
// (ADR 0043).
func (b *Bot) noteExternal(target botflota.ChatTarget, now time.Time) {
	b.controlMu.Lock()
	defer b.controlMu.Unlock()
	state := b.control[target]
	state.pausedTill = now.Add(b.externalPause)
	b.control[target] = state
}

// setHuman holds one chat until it is released, clearing any countdown; a
// release forgets the state (ADR 0043).
func (b *Bot) setHuman(target botflota.ChatTarget, human bool) {
	b.controlMu.Lock()
	defer b.controlMu.Unlock()
	if !human {
		delete(b.control, target)
		return
	}
	b.control[target] = chatControl{human: true}
}

// controlOf reports one chat's effective control for the read API (ADR 0043).
func (b *Bot) controlOf(target botflota.ChatTarget, now time.Time) botflota.ChatControl {
	b.controlMu.Lock()
	defer b.controlMu.Unlock()
	state, ok := b.control[target]
	if !ok {
		return botflota.ChatControl{Mode: botflota.ChatAuto}
	}
	if state.human {
		return botflota.ChatControl{Mode: botflota.ChatHuman}
	}
	if now.Before(state.pausedTill) {
		return botflota.ChatControl{
			Mode: botflota.ChatPaused, PausedTill: state.pausedTill,
		}
	}
	return botflota.ChatControl{Mode: botflota.ChatAuto}
}

// SetChatHuman holds or releases one chat's automation; taking the hold
// cancels the replies the bot still owes that chat, so a hand-over never sends
// an automatic reply already queued (ADR 0043). The bot's turn lock covers the
// cancellation and the hold together: a turn already past its pause check must
// not commit a fresh reply after the cancellation ran.
func (f *Fleet) SetChatHuman(
	ctx context.Context, scope, bot string, target botflota.ChatTarget, human bool,
) error {
	targetBot, ok := f.bot(scope, bot)
	if !ok || !targetBot.running.Load() {
		return fmt.Errorf("%w: %s/%s", botflota.ErrBotNotRunning, scope, bot)
	}
	targetBot.runtimeMu.Lock()
	defer targetBot.runtimeMu.Unlock()
	if human {
		if err := f.cancelChat(ctx, scope, bot, target); err != nil {
			return err
		}
	}
	targetBot.setHuman(target, human)
	return nil
}

// ChatControl reports one chat's live control; false means the bot is not
// running and holds no state (ADR 0043).
func (f *Fleet) ChatControl(
	scope, bot string, target botflota.ChatTarget,
) (botflota.ChatControl, bool) {
	found, ok := f.bot(scope, bot)
	if !ok || !found.running.Load() {
		return botflota.ChatControl{}, false
	}
	return found.controlOf(target, found.now()), true
}

// bot returns one built bot without holding the fleet lock.
func (f *Fleet) bot(scope, bot string) (*Bot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found, ok := f.bots[botflota.BotRef{Scope: scope, Bot: bot}]
	return found, ok
}

// cancelChat fails the pending replies addressed to one destination, so a
// hand-over never sends an automatic reply already queued there. The actual
// destination decides, not the chat whose history records the reply, which an
// alternate `to` may differ from (ADR 0043, ADR 0051).
func (f *Fleet) cancelChat(
	ctx context.Context, scope, bot string, target botflota.ChatTarget,
) error {
	count, err := f.opts.Outbox.FailPendingForTarget(ctx, scope, bot, target,
		"chat handed to a human", f.now())
	if err != nil {
		return fmt.Errorf("fleet: cancel pending replies: %w", err)
	}
	if count > 0 {
		f.opts.Log.Info("cancelled queued replies for a hand-over",
			"scope", scope, "bot", bot, "peer", target.Thread, "count", count)
	}
	return nil
}

func settingsOf(settings *spec.BotSettings) spec.BotSettings {
	if settings == nil {
		return spec.BotSettings{}
	}
	return *settings
}

// decodeOptions asks the definition for one bot's typed options. A definition
// without options decodes nothing (ADR 0052).
func decodeOptions(def channels.Definition, bot spec.Bot) (any, []string) {
	if def.Decode == nil {
		return nil, nil
	}
	return def.Decode(bot)
}
