// Package fleet runs one actor per configured bot, each with its own channel,
// engine, inbox filter, and outbox (ADR 0011, docs/architecture.md).
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

	"github.com/felixzsh/botforje/internal/config"
	"github.com/felixzsh/botforje/internal/domain"
	"github.com/felixzsh/botforje/internal/engine"
	"github.com/felixzsh/botforje/internal/inbox"
	"github.com/felixzsh/botforje/internal/logging"
	"github.com/felixzsh/botforje/internal/outbox"
)

// BotSpec identifies a bot to the channel factory.
type BotSpec struct {
	Scope    string
	Name     string
	Settings config.BotSettings
}

// Options configures a Fleet. Store and NewChannel are required.
type Options struct {
	Store          domain.SessionStore
	NewChannel     func(spec BotSpec) (domain.MessageChannel, error)
	HTTPClient     *http.Client
	Now            func() time.Time
	Rand           *rand.Rand
	DefaultTimeout time.Duration
	OnError        func(scope, bot string, err error)
	Log            *slog.Logger
}

// Fleet is the set of bot actors.
type Fleet struct {
	bots []*Bot
}

// BotStatus is one bot's runtime state.
type BotStatus struct {
	Scope       string
	Bot         string
	State       string
	OutboxDepth int
}

// Bot runs one bot: one channel, one engine, one outbox.
type Bot struct {
	scope   string
	name    string
	channel domain.MessageChannel
	engine  *engine.Engine
	outbox  *outbox.Queue
	filter  inbox.Filter
	onError func(error)
	log     *slog.Logger
}

// New builds the fleet from config. It starts nothing and connects nothing.
func New(cfg *config.Config, opts Options) (*Fleet, error) {
	if cfg == nil {
		return nil, errors.New("fleet: nil config")
	}
	if opts.Store == nil {
		return nil, errors.New("fleet: no session store")
	}
	if opts.NewChannel == nil {
		return nil, errors.New("fleet: no channel factory")
	}
	seeds := opts.Rand
	if seeds == nil {
		seeds = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	baseLog := opts.Log
	if baseLog == nil {
		baseLog = logging.Discard()
	}
	fleet := &Fleet{}
	for _, scopeName := range sortedScopes(cfg) {
		scope := cfg.Scopes[scopeName]
		for _, botName := range sortedBots(scope) {
			bot := scope.Bots[botName]
			graph, ok := scope.Graphs[bot.Graph]
			if !ok {
				return nil, fmt.Errorf("fleet: bot %q references unknown graph %q",
					botName, bot.Graph)
			}
			settings := settingsOf(bot.Settings)
			botLog := baseLog.With("scope", scopeName, "bot", botName)
			eng := engine.New(engine.Options{
				Bot: botName, Scope: scopeName, Graph: graph, Actions: scope.Actions,
				Store: opts.Store, HTTPClient: opts.HTTPClient, Now: opts.Now,
				Rand: rand.New(rand.NewSource(seeds.Int63())), DefaultTimeout: opts.DefaultTimeout,
				Log: botLog,
			})
			channel, err := opts.NewChannel(BotSpec{
				Scope: scopeName, Name: botName, Settings: settings,
			})
			if err != nil {
				return nil, fmt.Errorf("fleet: channel for %q: %w", botName, err)
			}
			queue := outbox.New(outbox.Config{
				Sender:       channel.Send,
				Delay:        time.Duration(settings.QueueDelay) * time.Millisecond,
				Jitter:       time.Duration(settings.QueueJitter) * time.Millisecond,
				MaxPerMinute: settings.MaxMsgPerMinute,
				Now:          opts.Now,
				Rand:         rand.New(rand.NewSource(seeds.Int63())),
				Log:          botLog,
				OnError: func(_ domain.OutgoingMessage, err error) {
					if opts.OnError != nil {
						opts.OnError(scopeName, botName, fmt.Errorf("send: %w", err))
					}
				},
			})
			fleet.bots = append(fleet.bots, &Bot{
				scope: scopeName, name: botName, channel: channel, engine: eng, outbox: queue,
				filter: inbox.Filter{
					IgnoreGroups: ignoreGroups(settings),
					Allowed:      settings.AllowedSenders,
					Ignored:      settings.IgnoredSenders,
				},
				log: botLog,
				onError: func(err error) {
					if opts.OnError != nil {
						opts.OnError(scopeName, botName, err)
					}
				},
			})
		}
	}
	return fleet, nil
}

// Run starts every bot and blocks until ctx is cancelled or all bots stop.
func (f *Fleet) Run(ctx context.Context) error {
	var wait sync.WaitGroup
	errs := make(chan error, len(f.bots))
	for _, bot := range f.bots {
		wait.Add(1)
		go func(b *Bot) {
			defer wait.Done()
			if err := b.run(ctx); err != nil {
				errs <- fmt.Errorf("%s/%s: %w", b.scope, b.name, err)
			}
		}(bot)
	}
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	select {
	case <-ctx.Done():
	case <-done:
	}
	wait.Wait()
	close(errs)
	var first error
	for err := range errs {
		if first == nil {
			first = err
		}
	}
	return first
}

// Status reports each bot's runtime state.
func (f *Fleet) Status() []BotStatus {
	statuses := make([]BotStatus, 0, len(f.bots))
	for _, bot := range f.bots {
		status := BotStatus{Scope: bot.scope, Bot: bot.name, OutboxDepth: bot.outbox.Depth()}
		if reporter, ok := bot.channel.(domain.StateReporter); ok {
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
				"sender", msg.Sender, "id", msg.ID, "group", msg.IsGroup, "body", msg.Body)
			if !b.filter.Allow(msg) {
				b.log.Debug("message filtered", "sender", msg.Sender)
				continue
			}
			replies, err := b.engine.Handle(ctx, msg)
			if err != nil {
				b.log.Error("engine failed", "sender", msg.Sender, "error", err)
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

func settingsOf(settings *config.BotSettings) config.BotSettings {
	if settings == nil {
		return config.BotSettings{}
	}
	return *settings
}

func ignoreGroups(settings config.BotSettings) bool {
	if settings.IgnoreGroups == nil {
		return true
	}
	return *settings.IgnoreGroups
}

func sortedScopes(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Scopes))
	for name := range cfg.Scopes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedBots(scope config.Scope) []string {
	names := make([]string, 0, len(scope.Bots))
	for name := range scope.Bots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
