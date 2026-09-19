package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/whatsapp"
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
		Skip: func(scope, bot string) bool {
			return skipped[scope][bot]
		},
		DefaultTimeout: time.Duration(cfg.DefaultTimeout) * time.Second,
		Log:            logger,
		NewChannel: func(spec fleet.BotSpec) (botflota.MessageChannel, error) {
			return whatsapp.New(whatsapp.Options{
				Scope:        spec.Scope,
				Bot:          spec.Name,
				Devices:      devices,
				Accounts:     db.LinkedAccounts(),
				Pairings:     db.Pairings(),
				Settings:     whatsappSettings(spec.Settings),
				Log:          logger.With("scope", spec.Scope, "bot", spec.Name),
				DepsLogLevel: botflota.ParseLogLevel(cfg.LogLevelDeps),
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

	if cfg.Port == 0 {
		if *cmd.newOwnerToken {
			logger.Error("--new-owner-token needs the API; port is 0")
			return 2
		}
	} else if err := serveAPI(ctx, stop, cfg, runner, db, logger, *cmd.newOwnerToken); err != nil {
		logger.Error("start api", "error", err)
		return 1
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

// serveAPI starts the REST API in the fleet process (ADR 0025). It returns
// after binding the address; serving continues until ctx is cancelled. A
// serving failure stops the fleet so the supervisor can restart it.
func serveAPI(
	ctx context.Context, stop context.CancelFunc, cfg *config.Config,
	runner *fleet.Fleet, db *sqlite.DB, logger *slog.Logger, rotate bool,
) error {
	dataDir, err := cfg.DataDirPath()
	if err != nil {
		return err
	}
	ownerHash, created, err := api.EnsureOwnerToken(ctx, db.OwnerTokens(), dataDir)
	if err != nil {
		return err
	}
	if created {
		logger.Warn("owner token created; the CLI reads it",
			"file", filepath.Join(dataDir, api.OwnerTokenFile))
	}
	if rotate {
		if ownerHash, err = api.RotateOwnerToken(ctx, db.OwnerTokens(), dataDir); err != nil {
			return err
		}
		logger.Warn("owner token rotated",
			"file", filepath.Join(dataDir, api.OwnerTokenFile))
	}
	server, err := api.New(api.Options{
		Config:   cfg,
		Fleet:    runner,
		Accounts: db.LinkedAccounts(),
		Pairings: db.Pairings(),
		Senders:  db.Senders(),
		Chats:    db.Chats(),
		Sessions: db.Sessions(),
		Messages: db.Messages(),
		Tokens:   db.AccessTokens(),
		Auth:     api.NewAuth(ownerHash, db.AccessTokens()),
		Log:      logger,
	})
	if err != nil {
		return err
	}
	listener, err := server.Listen()
	if err != nil {
		return err
	}
	logger.Info("api listening", "address", listener.Addr().String())
	go func() {
		if err := server.Serve(ctx, listener); err != nil {
			logger.Error("api failed", "error", err)
			stop()
		}
	}()
	return nil
}

func whatsappSettings(settings spec.BotSettings) whatsapp.Settings {
	return whatsapp.Settings{
		ReadReceipts:     settings.ReadReceipts != nil && *settings.ReadReceipts,
		ReadReceiptDelay: time.Duration(settings.ReadReceiptDelay) * time.Millisecond,
		ReplyDelay:       time.Duration(settings.ReplyDelay) * time.Millisecond,
		SimulateTyping:   settings.SimulateTyping != nil && *settings.SimulateTyping,
		Jitter:           time.Duration(settings.Jitter) * time.Millisecond,
	}
}
