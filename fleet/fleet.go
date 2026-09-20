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
	"sync"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/engine"
	"github.com/felixzsh/botflota/spec"
)

// BotSpec identifies a bot to the channel factory.
type BotSpec struct {
	Scope    string
	Name     string
	Settings spec.BotSettings
}

// Options configures a Fleet. Store, Chats, Senders, and NewChannel are
// required.
type Options struct {
	Store      botflota.SessionStore
	Chats      botflota.ChatStore
	Senders    botflota.SenderStore
	Messages   botflota.MessageStore
	NewChannel func(spec BotSpec) (botflota.MessageChannel, error)
	// Skip omits a bot before its runtime is built. It is for recoverable
	// runtime conditions such as a missing local pairing.
	Skip           func(scope, bot string) bool
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
	channel botflota.MessageChannel
	engine  *engine.Engine
	outbox  *outbox
	filter  Filter
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
	if opts.Chats == nil {
		return nil, errors.New("fleet: no chat store")
	}
	if opts.Senders == nil {
		return nil, errors.New("fleet: no sender store")
	}
	if opts.Messages == nil {
		return nil, errors.New("fleet: no message store")
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
		baseLog = botflota.DiscardLogger()
	}
	fleet := &Fleet{}
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
		scope := cfg.Scopes[scopeName]
		for _, botName := range config.SortedKeys(scope.Bots) {
			bot := scope.Bots[botName]
			graph, ok := scope.Graphs[bot.Graph]
			if !ok {
				return nil, fmt.Errorf("fleet: bot %q references unknown graph %q",
					botName, bot.Graph)
			}
			if opts.Skip != nil && opts.Skip(scopeName, botName) {
				continue
			}
			settings := settingsOf(bot.Settings)
			botLog := baseLog.With("scope", scopeName, "bot", botName)
			eng := engine.New(engine.Options{
				Bot: botName, Scope: scopeName, Graph: graph, Actions: scope.Actions,
				Store: opts.Store, Chats: opts.Chats, Senders: opts.Senders,
				Messages:   opts.Messages,
				HTTPClient: opts.HTTPClient, Now: opts.Now,
				Rand: rand.New(rand.NewSource(seeds.Int63())), DefaultTimeout: opts.DefaultTimeout,
				Log: botLog,
			})
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
					if msg.RecordID == 0 {
						return
					}
					if err := opts.Messages.SetProviderID(
						context.Background(), msg.RecordID, providerID,
					); err != nil {
						botLog.Warn("record outbound provider id", "error", err)
					}
				},
			})
			fleet.bots = append(fleet.bots, &Bot{
				scope: scopeName, name: botName, channel: channel, engine: eng, outbox: queue,
				filter: Filter{
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
				"sender", msg.Sender, "id", msg.ID, "group", msg.IsGroup,
				"kind", string(botflota.ContentKind(msg.Content)),
				"body", botflota.ContentText(msg.Content))
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
