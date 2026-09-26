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
	"sync"
	"sync/atomic"
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

// Store, Chats, Senders, Messages, Turn, Outbox, and NewChannel are required.
type Options struct {
	Store      botflota.SessionStore
	Chats      botflota.ChatStore
	Senders    botflota.SenderStore
	Messages   botflota.MessageStore
	Turn       botflota.TurnStore
	Outbox     botflota.OutboxStore
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
	mu      sync.Mutex
	applyMu sync.Mutex
	bots    map[string]*Bot
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
	control   map[string]chatControl
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
	if opts.Senders == nil {
		return nil, errors.New("fleet: no sender store")
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
	if opts.NewChannel == nil {
		return nil, errors.New("fleet: no channel factory")
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if opts.Log == nil {
		opts.Log = botflota.DiscardLogger()
	}
	fleet := &Fleet{
		cfg: cfg, opts: opts, bots: make(map[string]*Bot),
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
			fleet.bots[scopeName+"/"+botName] = built
		}
	}
	return fleet, nil
}

// buildBot constructs one configured bot's engine, channel, and outbox from
// the config the fleet was built with; it does not connect the channel or
// start the outbox worker.
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
		Store:        opts.Outbox,
		Scope:        scopeName,
		Bot:          botName,
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
	})
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Bot{
		scope: scopeName, name: botName, channel: channel, engine: eng, outbox: queue,
		filter: filter, fleet: f, gen: f.scopeGen[scopeName],
		externalPause: externalReplyPause(settings), now: now,
		control: map[string]chatControl{},
		stop:    make(chan struct{}), done: make(chan struct{}),
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
	key := scope + "/" + name
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
// whose credentials may be removed — one phone keeps one connection (ADR
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
	bot, ok := f.bots[scope+"/"+name]
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
	var outbound <-chan botflota.OutboundMessage
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
		"sender", msg.SenderPhone, "id", msg.ProviderMessageID,
		"group", msg.IsGroup,
		"kind", string(botflota.ContentKind(msg.Content)),
		"body", botflota.ContentText(msg.Content))
	if err := b.fleet.refresh(b); err != nil {
		b.log.Warn("apply config change", "error", err)
	}
	if !b.filter.Allow(msg) {
		b.log.Debug("message filtered", "sender", msg.SenderPhone)
		return
	}
	if b.paused(msg.SenderPhone, b.now()) {
		b.log.Info("chat held by a human; message stored without a reply",
			"sender", msg.SenderPhone)
		if err := b.engine.RecordSuppressed(ctx, msg); err != nil {
			b.log.Error("record held message", "sender", msg.SenderPhone, "error", err)
			b.onError(err)
		}
		return
	}
	replies, err := b.engine.Handle(ctx, msg)
	if err != nil {
		b.log.Error("engine failed", "sender", msg.SenderPhone, "error", err)
		b.onError(err)
		return
	}
	if len(replies) > 0 {
		b.outbox.Wake()
	}
}

// handleExternal records a reply the operator sent from another client and
// hands its chat over for the bot's configured window (ADR 0043).
func (b *Bot) handleExternal(ctx context.Context, out botflota.OutboundMessage) {
	b.runtimeMu.Lock()
	defer b.runtimeMu.Unlock()
	// Cancel what the bot still owes the chat before pausing it, so no queued
	// reply leaves after the operator's own (ADR 0043).
	if err := b.fleet.cancelChat(ctx, b.scope, b.name, out.RecipientPhone); err != nil {
		b.log.Warn("cancel queued replies for a hand-over",
			"sender", out.RecipientPhone, "error", err)
	}
	b.noteExternal(out.RecipientPhone, b.now())
	if err := b.engine.RecordOperator(ctx, out); err != nil {
		b.log.Warn("record operator reply",
			"sender", out.RecipientPhone, "error", err)
	}
	b.log.Info("chat handed to a human",
		"sender", out.RecipientPhone, "pause", b.externalPause)
}

// paused reports whether one chat is held right now: an indefinite manual hold
// or an automatic countdown still running. A finished countdown is forgotten,
// so the map does not grow with every chat that was ever paused (ADR 0043).
func (b *Bot) paused(sender string, now time.Time) bool {
	b.controlMu.Lock()
	defer b.controlMu.Unlock()
	state, ok := b.control[sender]
	if !ok {
		return false
	}
	if state.human {
		return true
	}
	if now.Before(state.pausedTill) {
		return true
	}
	delete(b.control, sender)
	return false
}

// noteExternal restarts one chat's hand-over countdown after an external reply
// (ADR 0043).
func (b *Bot) noteExternal(sender string, now time.Time) {
	b.controlMu.Lock()
	defer b.controlMu.Unlock()
	state := b.control[sender]
	state.pausedTill = now.Add(b.externalPause)
	b.control[sender] = state
}

// setHuman holds one chat until it is released, clearing any countdown; a
// release forgets the state (ADR 0043).
func (b *Bot) setHuman(sender string, human bool) {
	b.controlMu.Lock()
	defer b.controlMu.Unlock()
	if !human {
		delete(b.control, sender)
		return
	}
	b.control[sender] = chatControl{human: true}
}

// controlOf reports one chat's effective control for the read API (ADR 0043).
func (b *Bot) controlOf(sender string, now time.Time) botflota.ChatControl {
	b.controlMu.Lock()
	defer b.controlMu.Unlock()
	state, ok := b.control[sender]
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
// an automatic reply already queued (ADR 0043).
func (f *Fleet) SetChatHuman(
	ctx context.Context, scope, bot, phone string, human bool,
) error {
	target, ok := f.bot(scope, bot)
	if !ok || !target.running.Load() {
		return fmt.Errorf("fleet: bot %s/%s is not running", scope, bot)
	}
	if human {
		if err := f.cancelChat(ctx, scope, bot, phone); err != nil {
			return err
		}
	}
	target.setHuman(phone, human)
	return nil
}

// ChatControl reports one chat's live control; false means the bot is not
// running and holds no state (ADR 0043).
func (f *Fleet) ChatControl(scope, bot, phone string) (botflota.ChatControl, bool) {
	target, ok := f.bot(scope, bot)
	if !ok || !target.running.Load() {
		return botflota.ChatControl{}, false
	}
	return target.controlOf(phone, target.now()), true
}

// bot returns one built bot without holding the fleet lock.
func (f *Fleet) bot(scope, bot string) (*Bot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found, ok := f.bots[scope+"/"+bot]
	return found, ok
}

// cancelChat fails the pending replies stored under one chat, so a hand-over
// never sends an automatic reply already queued (ADR 0043).
func (f *Fleet) cancelChat(ctx context.Context, scope, bot, phone string) error {
	sender, err := f.opts.Senders.Get(ctx, scope, phone)
	if errors.Is(err, botflota.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fleet: sender for control: %w", err)
	}
	chat, err := f.opts.Chats.ForSender(ctx, scope, bot, sender.ID)
	if errors.Is(err, botflota.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fleet: chat for control: %w", err)
	}
	count, err := f.opts.Outbox.FailPendingForChat(ctx, chat.ID,
		"chat handed to a human", f.now())
	if err != nil {
		return fmt.Errorf("fleet: cancel pending replies: %w", err)
	}
	if count > 0 {
		f.opts.Log.Info("cancelled queued replies for a hand-over",
			"scope", scope, "bot", bot, "sender", phone, "count", count)
	}
	return nil
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
