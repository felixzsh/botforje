package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/felixzsh/botforje/internal/config"
	"github.com/felixzsh/botforje/internal/domain"
	"github.com/felixzsh/botforje/internal/fleet"
	"github.com/felixzsh/botforje/internal/logging"
	"github.com/felixzsh/botforje/internal/whatsapp"
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
	if err := whatsapp.ValidateAccounts(ctx, db.DB, db.Accounts()); err != nil {
		logger.Error("validate pairings", "error", err)
		return 1
	}
	skipped := make(map[string]map[string]bool)
	skippedCount := 0
	for _, scopeName := range sortedScopeNames(cfg) {
		for _, botName := range sortedBotNames(cfg.Scopes[scopeName]) {
			state, err := whatsapp.CheckPairing(ctx, db.DB, db.Accounts(),
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
		Store: db.Sessions(),
		Skip: func(scope, bot string) bool {
			return skipped[scope][bot]
		},
		DefaultTimeout: time.Duration(cfg.DefaultTimeout) * time.Second,
		Log:            logger,
		NewChannel: func(spec fleet.BotSpec) (domain.MessageChannel, error) {
			return whatsapp.New(whatsapp.Options{
				Scope:        spec.Scope,
				Bot:          spec.Name,
				DB:           db.DB,
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
	typing := 1200 * time.Millisecond
	if settings.TypingDelay > 0 {
		typing = time.Duration(settings.TypingDelay) * time.Millisecond
	}
	return whatsapp.Settings{
		ReadReceipts:   settings.ReadReceipts != nil && *settings.ReadReceipts,
		SimulateTyping: settings.SimulateTyping != nil && *settings.SimulateTyping,
		TypingDelay:    typing,
	}
}
