package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
	"github.com/felixzsh/botflota/spec"
)

type fleetRunCmd struct {
	fs     *flag.FlagSet
	config *string
}

func newFleetRunCmd() *fleetRunCmd {
	fs := flag.NewFlagSet("fleet run", flag.ContinueOnError)
	fs.Usage = func() {}
	return &fleetRunCmd{fs: fs, config: configFlag(fs)}
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

func whatsappSettings(settings spec.BotSettings) whatsapp.Settings {
	return whatsapp.Settings{
		ReadReceipts:     settings.ReadReceipts != nil && *settings.ReadReceipts,
		ReadReceiptDelay: time.Duration(settings.ReadReceiptDelay) * time.Millisecond,
		ReplyDelay:       time.Duration(settings.ReplyDelay) * time.Millisecond,
		SimulateTyping:   settings.SimulateTyping != nil && *settings.SimulateTyping,
		Jitter:           time.Duration(settings.Jitter) * time.Millisecond,
	}
}
