package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/installed"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/egress"
	"github.com/felixzsh/botflota/spec"
)

// pureCatalog is the catalog config-only commands validate against: the
// installed declarations without runtime hooks, so validation opens no store
// and starts no transport (ADR 0052, ADR 0054).
func pureCatalog() (*channels.Catalog, error) {
	return channels.New(pureDefinitions(installed.Channels())...)
}

// pureDefinitions is the installed set, one declaration per provider and
// mode, without any runtime hook.
func pureDefinitions(installations []channels.Installation) []channels.Definition {
	defs := make([]channels.Definition, 0, len(installations))
	for _, installation := range installations {
		defs = append(defs, installation.Definition)
	}
	return defs
}

// selectedKeys resolves every selector the config names through the installed
// catalog and returns the keys a fleet runs. The catalog decides what is
// installed, so an incomplete, unknown, or unavailable selection selects
// nothing (ADR 0052, ADR 0054).
func selectedKeys(catalog *channels.Catalog, cfg *config.Config) map[channels.Key]bool {
	keys := map[channels.Key]bool{}
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
		for _, botName := range config.SortedKeys(cfg.Scopes[scopeName].Bots) {
			selector := cfg.Scopes[scopeName].Bots[botName].Channel
			if _, key, err := catalog.Resolve(selector); err == nil {
				keys[key] = true
			}
		}
	}
	return keys
}

// serves reports whether this registration provides one installed key.
func serves(installation channels.Installation, key channels.Key) bool {
	if installation.Definition.Provider != key.Provider {
		return false
	}
	for _, mode := range installation.Definition.Modes {
		if key.Mode == mode {
			return true
		}
	}
	return false
}

// selectedInstallation reports whether the config runs a bot on one of this
// registration's keys, so only a selected adapter's resources are opened
// (ADR 0052, ADR 0054).
func selectedInstallation(
	installation channels.Installation, selected map[channels.Key]bool,
) bool {
	for key := range selected {
		if serves(installation, key) {
			return true
		}
	}
	return false
}

// runtimeSet is the resources the installed adapters opened. Close is
// idempotent and releases them in reverse order, so a command and the API can
// both release them before the stores they write go away (ADR 0053).
type runtimeSet struct {
	installed []channels.Installed
	once      sync.Once
}

// validate runs each installed adapter's durable-state check.
func (s *runtimeSet) validate(ctx context.Context) error {
	for _, runtime := range s.installed {
		if runtime.Validate == nil {
			continue
		}
		if err := runtime.Validate(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Close releases every opened adapter resource once.
func (s *runtimeSet) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		for i := len(s.installed) - 1; i >= 0; i-- {
			if s.installed[i].Close != nil {
				s.installed[i].Close()
			}
		}
	})
}

// channelHost is the neutral context the installed adapters receive: the
// shared stores plus the root's own services.
func channelHost(
	cfg *config.Config, db *sqlite.DB, log *slog.Logger,
	bot func(scope, name string) (spec.Bot, error),
	startBot func(ctx context.Context, scope, bot string) error,
) (channels.Host, error) {
	client, err := outboundClient(cfg)
	if err != nil {
		return channels.Host{}, err
	}
	return channels.Host{
		DB:           db.DB,
		Bindings:     db.Bindings(),
		Peers:        db.Peers(),
		Chats:        db.Chats(),
		Messages:     db.Messages(),
		Directory:    db.Directory(),
		Outbox:       db.Outbox(),
		Log:          log,
		HTTPClient:   client,
		DepsLogLevel: botflota.ParseLogLevel(cfg.LogLevelDeps),
		Bot:          bot,
		StartBot:     startBot,
	}, nil
}

// channelRuntime installs the adapters cfg selects. Only a selected adapter's
// Install runs, so an instance that selects none opens nothing; every
// installed declaration is still registered, so selecting one needs no
// provider switch here (ADR 0052, ADR 0054).
func channelRuntime(
	ctx context.Context, cfg *config.Config, db *sqlite.DB, log *slog.Logger,
	bot func(scope, name string) (spec.Bot, error),
	startBot func(ctx context.Context, scope, bot string) error,
	installations []channels.Installation,
) (*channels.Catalog, *runtimeSet, error) {
	pure, err := channels.New(pureDefinitions(installations)...)
	if err != nil {
		return nil, nil, err
	}
	selected := selectedKeys(pure, cfg)
	host, err := channelHost(cfg, db, log, bot, startBot)
	if err != nil {
		return nil, nil, err
	}
	defs := make([]channels.Definition, 0, len(installations))
	set := &runtimeSet{}
	for _, installation := range installations {
		def := installation.Definition
		if installation.Install != nil && selectedInstallation(installation, selected) {
			runtime, err := installation.Install(ctx, host)
			if err != nil {
				set.Close()
				return nil, nil, err
			}
			def = runtime.Definition
			set.installed = append(set.installed, runtime)
		}
		defs = append(defs, def)
	}
	catalog, err := channels.New(defs...)
	if err != nil {
		set.Close()
		return nil, nil, err
	}
	return catalog, set, nil
}

// outboundClient is the client every configurable outbound fetch uses: a
// request step, media fetched by url, an adapter's own fetch, and a webhook
// delivery, so all of them obey the instance egress policy (ADR 0048).
func outboundClient(cfg *config.Config) (*http.Client, error) {
	mode, err := egress.ParseMode(cfg.Egress)
	if err != nil {
		return nil, err
	}
	return egress.Client(mode)
}

// configBotLookup resolves one bot from a loaded file, so an adapter's setup
// path reads the same document the command validated.
func configBotLookup(cfg *config.Config) func(scope, bot string) (spec.Bot, error) {
	return func(scope, bot string) (spec.Bot, error) {
		return lookupBot(cfg, scope, bot)
	}
}

// liveBotLookup resolves one bot from the running snapshot, so a reload
// reaches a setup session too (ADR 0009, ADR 0053).
func liveBotLookup(source *config.Source) func(scope, bot string) (spec.Bot, error) {
	return func(scope, bot string) (spec.Bot, error) {
		return lookupBot(source.Current(), scope, bot)
	}
}

func lookupBot(cfg *config.Config, scope, name string) (spec.Bot, error) {
	scopeCfg, ok := cfg.Scopes[scope]
	if !ok {
		return spec.Bot{}, fmt.Errorf("unknown scope %q", scope)
	}
	bot, ok := scopeCfg.Bots[name]
	if !ok {
		return spec.Bot{}, fmt.Errorf("bot %q not found in scope %q", name, scope)
	}
	return bot, nil
}
