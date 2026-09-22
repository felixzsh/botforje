package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/adapters/whatsapp/channel"
	"github.com/felixzsh/botflota/api"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
	"github.com/felixzsh/botflota/spec"
)

type fleetRunCmd struct {
	fs            *flag.FlagSet
	config        *string
	newOwnerToken *bool
}

func newFleetRunCmd() *fleetRunCmd {
	fs := flag.NewFlagSet("fleet run", flag.ContinueOnError)
	fs.Usage = func() {}
	return &fleetRunCmd{
		fs:     fs,
		config: configFlag(fs),
		newOwnerToken: fs.Bool("new-owner-token", false,
			"rotate the owner token and rewrite the token file"),
	}
}

func runFleet(args []string) int {
	cmd := newFleetRunCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	logger := botflota.NewLogger(cfg.LogLevel, os.Stderr)
	source := config.NewSource(cfg)

	lock, err := lockInstance(cfg)
	if err != nil {
		logger.Error("own instance", "error", err)
		return 1
	}
	defer releaseInstance(lock, func(err error) {
		logger.Error("release instance", "error", err)
	})

	db, err := openDatabase(cfg)
	if err != nil {
		logger.Error("open database", "error", err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	devices, err := openDevices(ctx, db, logger, cfg.LogLevelDeps)
	if err != nil {
		logger.Error("open device store", "error", err)
		return 1
	}
	if err := whatsapp.ValidateAccounts(ctx, devices, db.LinkedAccounts(),
		db.Pairings()); err != nil {
		logger.Error("validate pairings", "error", err)
		return 1
	}
	skipped := make(map[string]map[string]bool)
	skippedCount := 0
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
		for _, botName := range config.SortedKeys(cfg.Scopes[scopeName].Bots) {
			state, err := whatsapp.CheckPairing(ctx, devices, db.LinkedAccounts(),
				db.Pairings(), scopeName, botName)
			if err != nil {
				logger.Error("validate pairing", "scope", scopeName,
					"bot", botName, "error", err)
				return 1
			}
			if state == botflota.PairingPaired {
				continue
			}
			if skipped[scopeName] == nil {
				skipped[scopeName] = make(map[string]bool)
			}
			skipped[scopeName][botName] = true
			skippedCount++
			logger.Warn("bot not initialized; use bot pair",
				"scope", scopeName, "bot", botName, "state", string(state))
		}
	}

	runner, err := fleet.New(cfg, fleet.Options{
		Store:    db.Sessions(),
		Chats:    db.Chats(),
		Senders:  db.Senders(),
		Messages: db.Messages(),
		Turn:     db.Turns(),
		Skip: func(scope, bot string) bool {
			return skipped[scope][bot]
		},
		DefaultTimeout: time.Duration(cfg.DefaultTimeout) * time.Second,
		Log:            logger,
		NewChannel: func(botSpec fleet.BotSpec) (botflota.MessageChannel, error) {
			return channel.New(channel.Options{
				Scope:          botSpec.Scope,
				Bot:            botSpec.Name,
				Devices:        devices,
				Accounts:       db.LinkedAccounts(),
				Pairings:       db.Pairings(),
				Senders:        db.Senders(),
				Chats:          db.Chats(),
				Messages:       db.Messages(),
				History:        historyEnabled(&botSpec.Settings),
				Contacts:       db.Contacts(),
				ImportContacts: contactsEnabled(&botSpec.Settings),
				Settings:       whatsappSettings(botSpec.Settings),
				Log:            logger.With("scope", botSpec.Scope, "bot", botSpec.Name),
				DepsLogLevel:   botflota.ParseLogLevel(cfg.LogLevelDeps),
			})
		},
	})
	if err != nil {
		logger.Error("build fleet", "error", err)
		return 1
	}

	statuses := runner.Status()
	logger.Info("fleet starting", "bots", len(statuses), "skipped", skippedCount)
	for _, status := range statuses {
		logger.Debug("bot configured", "scope", status.Scope, "bot", status.Bot)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		logger.Info("shutdown requested")
	}()
	if watchFile, err := configPath(*cmd.config); err != nil {
		logger.Warn("config watch unavailable", "error", err)
	} else {
		go watchConfig(ctx, watchFile, source, runner, logger)
	}

	if cfg.Port == 0 {
		if *cmd.newOwnerToken {
			logger.Error("--new-owner-token needs the API; port is 0")
			return 2
		}
	} else {
		stopAPI, err := serveAPI(ctx, stop, cfg, source, runner, db, devices,
			logger, *cmd.newOwnerToken)
		if err != nil {
			logger.Error("start api", "error", err)
			return 1
		}
		// Registered after db.Close, so it runs first: serving and pairing
		// finish before the database they write goes away.
		defer stopAPI()
	}

	if len(statuses) == 0 {
		logger.Warn("no paired bots; fleet is idle")
		<-ctx.Done()
		logger.Info("fleet stopped")
		return 0
	}
	if err := runner.Run(ctx); err != nil {
		return 1
	}
	logger.Info("fleet stopped")
	return 0
}

type fleetStatusCmd struct {
	fs     *flag.FlagSet
	config *string
	json   *bool
}

func newFleetStatusCmd() *fleetStatusCmd {
	fs := flag.NewFlagSet("fleet status", flag.ContinueOnError)
	fs.Usage = func() {}
	return &fleetStatusCmd{
		fs:     fs,
		config: configFlag(fs),
		json:   fs.Bool("json", false, "machine-readable output"),
	}
}

func runFleetStatus(args []string) int {
	cmd := newFleetStatusCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	client, err := newAPIClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx := context.Background()
	if *cmd.json {
		body, err := client.getRaw(ctx, "/api/v1/status")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_, _ = os.Stdout.Write(body)
		return 0
	}
	var status struct {
		UptimeSeconds int `json:"uptime_seconds"`
		Bots          []struct {
			Bot      string `json:"bot"`
			Scope    string `json:"scope"`
			State    string `json:"state"`
			Outbox   int    `json:"outbox"`
			Sessions int    `json:"sessions"`
		} `json:"bots"`
	}
	if err := client.get(ctx, "/api/v1/status", &status); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	uptime := time.Duration(status.UptimeSeconds) * time.Second
	fmt.Printf("uptime %s\n", uptime)
	fmt.Printf("%-24s %-12s %-12s %6s %8s\n",
		"BOT", "SCOPE", "STATE", "OUTBOX", "SESSIONS")
	for _, bot := range status.Bots {
		fmt.Printf("%-24s %-12s %-12s %6d %8d\n",
			bot.Bot, bot.Scope, bot.State, bot.Outbox, bot.Sessions)
	}
	return 0
}

// serveAPI binds the address and serves in the background until ctx is
// cancelled; a serving failure stops the fleet so the supervisor can restart
// it. The returned cleanup cancels serving, waits for it, and closes the
// pairing manager, so nothing writes the stores after the caller closes them;
// defer it after db.Close so it runs first.
func serveAPI(
	ctx context.Context, stop context.CancelFunc, cfg *config.Config,
	source *config.Source, runner *fleet.Fleet, db *sqlite.DB,
	devices *whatsapp.Devices, logger *slog.Logger, rotate bool,
) (func(), error) {
	dataDir, err := cfg.DataDirPath()
	if err != nil {
		return nil, err
	}
	ownerHash, created, err := api.EnsureOwnerToken(ctx, db.OwnerTokens(), dataDir)
	if err != nil {
		return nil, err
	}
	if created {
		logger.Warn("owner token created; the CLI reads it",
			"file", filepath.Join(dataDir, api.OwnerTokenFile))
	}
	if rotate {
		if ownerHash, err = api.RotateOwnerToken(ctx, db.OwnerTokens(), dataDir); err != nil {
			return nil, err
		}
		logger.Warn("owner token rotated",
			"file", filepath.Join(dataDir, api.OwnerTokenFile))
	}
	pairings := pairingManager(source, db, devices, runner, logger)
	server, err := api.New(api.Options{
		Config:   source,
		Fleet:    runner,
		Accounts: db.LinkedAccounts(),
		Pairings: db.Pairings(),
		Pairing:  pairings,
		Presence: devices,
		Senders:  db.Senders(),
		Contacts: db.Contacts(),
		Chats:    db.Chats(),
		Sessions: db.Sessions(),
		Messages: db.Messages(),
		Tokens:   db.AccessTokens(),
		Resolver: api.NewAuth(ownerHash, db.AccessTokens()),
		Log:      logger,
	})
	if err != nil {
		pairings.Close()
		return nil, err
	}
	listener, err := server.Listen()
	if err != nil {
		pairings.Close()
		return nil, err
	}
	logger.Info("api listening", "address", listener.Addr().String())
	served := make(chan struct{})
	go func() {
		defer close(served)
		if err := server.Serve(ctx, listener); err != nil {
			logger.Error("api failed", "error", err)
			stop()
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			stop()
			<-served
			pairings.Close()
		})
	}, nil
}

func whatsappSettings(settings spec.BotSettings) channel.Settings {
	return channel.Settings{
		ReadReceipts:     settings.ReadReceipts != nil && *settings.ReadReceipts,
		ReadReceiptDelay: time.Duration(settings.ReadReceiptDelay) * time.Millisecond,
		ReplyDelay:       time.Duration(settings.ReplyDelay) * time.Millisecond,
		SimulateTyping:   settings.SimulateTyping != nil && *settings.SimulateTyping,
		Jitter:           time.Duration(settings.Jitter) * time.Millisecond,
	}
}

// pairingManager is the API path of `bot pair`: the session it runs resolves
// the bot's settings against the current config snapshot, so a reload reaches
// it too, and starts the bot when WhatsApp accepts the scan (ADR 0025).
func pairingManager(
	source *config.Source, db *sqlite.DB, devices *whatsapp.Devices,
	runner *fleet.Fleet, logger *slog.Logger,
) *whatsapp.PairManager {
	return whatsapp.NewPairManager(whatsapp.PairManagerOptions{
		Build: func(scope, botName string, fullHistory bool) (whatsapp.PairOptions, error) {
			cfg := source.Current()
			scopeCfg, ok := cfg.Scopes[scope]
			if !ok {
				return whatsapp.PairOptions{}, fmt.Errorf("unknown scope %q", scope)
			}
			bot, ok := scopeCfg.Bots[botName]
			if !ok {
				return whatsapp.PairOptions{}, fmt.Errorf(
					"bot %q not found in scope %q", botName, scope)
			}
			return whatsapp.PairOptions{
				Scope:          scope,
				Bot:            botName,
				Devices:        devices,
				Accounts:       db.LinkedAccounts(),
				Pairings:       db.Pairings(),
				Senders:        db.Senders(),
				Chats:          db.Chats(),
				Messages:       db.Messages(),
				History:        historyEnabled(bot.Settings),
				FullHistory:    fullHistory,
				Contacts:       db.Contacts(),
				ImportContacts: contactsEnabled(bot.Settings),
				Log:            logger,
				DepsLogLevel:   botflota.ParseLogLevel(cfg.LogLevelDeps),
				DeviceName:     cfg.DeviceName,
			}, nil
		},
		OnPaired: func(_ context.Context, scope, botName string) error {
			return runner.StartBot(scope, botName)
		},
		Log: logger,
	})
}
