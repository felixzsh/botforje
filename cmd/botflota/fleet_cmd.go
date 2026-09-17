package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/felixzsh/botflota/internal/config"
	"github.com/felixzsh/botflota/internal/domain"
	"github.com/felixzsh/botflota/internal/fleet"
	"github.com/felixzsh/botflota/internal/logging"
	"github.com/felixzsh/botflota/internal/whatsapp"
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
	logger := logging.New(cfg.LogLevel, os.Stderr)

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
	if err := whatsapp.ValidateAccounts(ctx, devices, db.Accounts()); err != nil {
		logger.Error("validate pairings", "error", err)
		return 1
	}
	skipped := make(map[string]map[string]bool)
	skippedCount := 0
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
		for _, botName := range config.SortedKeys(cfg.Scopes[scopeName].Bots) {
			state, err := whatsapp.CheckPairing(ctx, devices, db.Accounts(),
				scopeName, botName)
			if err != nil {
				logger.Error("validate pairing", "scope", scopeName,
					"bot", botName, "error", err)
				return 1
			}
			if state == domain.PairingPaired {
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
		Store:   db.Sessions(),
		Chats:   db.Chats(),
		Senders: db.Senders(),
		Skip: func(scope, bot string) bool {
			return skipped[scope][bot]
		},
		DefaultTimeout: time.Duration(cfg.DefaultTimeout) * time.Second,
		Log:            logger,
		NewChannel: func(spec fleet.BotSpec) (domain.MessageChannel, error) {
			return whatsapp.New(whatsapp.Options{
				Scope:        spec.Scope,
				Bot:          spec.Name,
				Devices:      devices,
				Accounts:     db.Accounts(),
				Settings:     whatsappSettings(spec.Settings),
				Log:          logger.With("scope", spec.Scope, "bot", spec.Name),
				DepsLogLevel: logging.ParseLevel(cfg.LogLevelDeps),
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

func whatsappSettings(settings config.BotSettings) whatsapp.Settings {
	return whatsapp.Settings{
		ReadReceipts:     settings.ReadReceipts != nil && *settings.ReadReceipts,
		ReadReceiptDelay: time.Duration(settings.ReadReceiptDelay) * time.Millisecond,
		ReplyDelay:       time.Duration(settings.ReplyDelay) * time.Millisecond,
		SimulateTyping:   settings.SimulateTyping != nil && *settings.SimulateTyping,
		Jitter:           time.Duration(settings.Jitter) * time.Millisecond,
	}
}
