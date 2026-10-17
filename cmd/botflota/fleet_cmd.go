package main

import (
	"context"
	"errors"
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
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
	"github.com/felixzsh/botflota/webhook"
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
	// The path is resolved, the instance is taken, and only then is the tree
	// loaded: recovery of an interrupted publication runs under the lock, so two
	// processes never restore the same files at once (ADR 0039).
	configFile, err := configPath(*cmd.config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	lock, err := ownInstance(configFile)
	if err != nil {
		reportOwned(err, "starting the fleet")
		return 1
	}
	defer releaseInstance(lock, func(err error) {
		fmt.Fprintln(os.Stderr, "release instance:", err)
	})

	cfg, code := loadConfig(configFile)
	if code != 0 {
		return code
	}
	logger := botflota.NewLogger(cfg.LogLevel, os.Stderr)
	source := config.NewSource(cfg)
	// saved tracks the files, which run ahead of the running instance while a
	// change is valid but not yet active (ADR 0009).
	saved := config.NewSource(cfg)
	// tree writes scope configuration back into the files, and holds them still
	// while they are read, so the watcher and a publication never overlap. The
	// writer validates candidates against the pure catalog: publications are
	// config-only and perform no channel I/O (ADR 0054).
	pure, err := pureCatalog()
	if err != nil {
		logger.Error("build channel catalog", "error", err)
		return 1
	}
	tree, err := config.NewWriter(cfg.SourcePath(), pure)
	if err != nil {
		logger.Error("open config writer", "error", err)
		return 1
	}

	db, err := openDatabase(cfg)
	if err != nil {
		logger.Error("open database", "error", err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	// Only the resources a selected adapter needs are opened: a fleet that runs
	// no WhatsApp bot opens no WhatsApp device store. Every installed adapter's
	// definition is still registered, so selecting one needs no provider switch
	// here (ADR 0052, ADR 0054).
	var devices *whatsapp.Devices
	var catalog *channels.Catalog
	var runner *fleet.Fleet
	var manager *whatsapp.PairManager
	if whatsappSelected(cfg) {
		devices, err = openDevices(ctx, db, logger, cfg.LogLevelDeps)
		if err != nil {
			logger.Error("open device store", "error", err)
			return 1
		}
		if err := whatsapp.ValidateAccountOwners(ctx, db.Bindings()); err != nil {
			logger.Error("validate account ownership", "error", err)
			return 1
		}
		// The setup runner is assembled with the catalog: its activation
		// reaches the fleet through a late binding, because the fleet is
		// built from this same catalog below (ADR 0053).
		manager = pairingManager(source, db, devices,
			func(ctx context.Context, scope, bot string) error {
				if runner == nil {
					return errors.New("fleet is not running")
				}
				return runner.StartBot(scope, bot)
			}, logger)
		// Setup workers stop before the stores they write go away (ADR 0053).
		defer manager.Close()
	}
	catalog, err = runtimeCatalog(channel.Deps{
		Devices:      devices,
		Bindings:     db.Bindings(),
		Peers:        db.Peers(),
		Chats:        db.Chats(),
		Messages:     db.Messages(),
		Directory:    db.Directory(),
		Outbox:       db.Outbox(),
		Pairs:        manager,
		DepsLogLevel: botflota.ParseLogLevel(cfg.LogLevelDeps),
	})
	if err != nil {
		logger.Error("build channel catalog", "error", err)
		return 1
	}

	runner, err = fleet.New(cfg, fleet.Options{
		Store: db.Sessions(),
		Chats: db.Chats(), Peers: db.Peers(),
		Messages:       db.Messages(),
		Turn:           db.Turns(),
		Outbox:         db.Outbox(),
		Bindings:       db.Bindings(),
		Notify:         webhook.NewSink(db.Webhooks(), webhook.Targets(cfg.Webhooks)),
		DefaultTimeout: time.Duration(cfg.DefaultTimeout) * time.Second,
		Log:            logger,
		Catalog:        catalog,
	})
	if err != nil {
		logger.Error("build fleet", "error", err)
		return 1
	}

	statuses := runner.Status()
	logger.Info("fleet starting", "bots", len(statuses))
	for _, status := range statuses {
		logger.Debug("bot configured", "scope", status.Scope, "bot", status.Bot)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		logger.Info("shutdown requested")
	}()
	// Webhook deliveries stop with the process; the queue is durable, so a
	// delivery still pending after this run is retried by the next one
	// (ADR 0045).
	deliveries := webhook.NewWorker(db.Webhooks(), webhook.Targets(cfg.Webhooks),
		webhook.WorkerOptions{Log: logger})
	deliveries.Start(ctx)
	defer func() {
		stop()
		deliveries.Wait()
	}()
	// The watcher reads the same tree the writer replaces, so both see one
	// document rather than two paths that could drift.
	go watchConfig(ctx, tree.MainPath(), source, saved, tree, runner, catalog, logger)

	if cfg.Port == 0 {
		if *cmd.newOwnerToken {
			logger.Error("--new-owner-token needs the API; port is 0")
			return 2
		}
	} else {
		stopAPI, err := serveAPI(ctx, stop, cfg, source, saved, tree, runner, db,
			devices, manager, catalog, logger, *cmd.newOwnerToken)
		if err != nil {
			logger.Error("start api", "error", err)
			return 1
		}
		// Registered after db.Close, so it runs first: serving and pairing
		// finish before the database they write goes away.
		defer stopAPI()
	}

	if len(statuses) == 0 {
		logger.Warn("no bound bots; fleet is idle")
	}
	// Run owns the instance's life: it also waits out an empty fleet, so a
	// pairing through the API can start the first bot (ADR 0022).
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
			Bot       string `json:"bot"`
			Scope     string `json:"scope"`
			State     string `json:"state"`
			Outbox    int    `json:"outbox"`
			GraphRuns int    `json:"graph_runs"`
		} `json:"bots"`
	}
	if err := client.get(ctx, "/api/v1/status", &status); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	uptime := time.Duration(status.UptimeSeconds) * time.Second
	fmt.Printf("uptime %s\n", uptime)
	fmt.Printf("%-24s %-12s %-12s %6s %8s\n",
		"BOT", "SCOPE", "STATE", "OUTBOX", "GRAPH RUNS")
	for _, bot := range status.Bots {
		fmt.Printf("%-24s %-12s %-12s %6d %8d\n",
			bot.Bot, bot.Scope, bot.State, bot.Outbox, bot.GraphRuns)
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
	source, saved *config.Source, tree *config.Writer, runner *fleet.Fleet,
	db *sqlite.DB, devices *whatsapp.Devices, manager *whatsapp.PairManager,
	catalog *channels.Catalog, logger *slog.Logger, rotate bool,
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
	var closeSetup func()
	if devices != nil {
		closeSetup = manager.Close
	}
	budgets := cfg.RateBudgets()
	server, err := api.New(api.Options{
		Config:      source,
		Saved:       saved,
		Writer:      tree,
		Applier:     runner,
		Fleet:       runner,
		Setups:      runner,
		Bindings:    db.Bindings(),
		Peers:       db.Peers(),
		Directory:   db.Directory(),
		Chats:       db.Chats(),
		Sessions:    db.Sessions(),
		Messages:    db.Messages(),
		Sends:       db.Sends(),
		Channels:    catalog,
		History:     db.History(),
		Pauser:      runner,
		Tokens:      db.AccessTokens(),
		RateBudgets: &budgets,
		Resolver:    api.NewAuth(ownerHash, db.AccessTokens()),
		Log:         logger,
	})
	if err != nil {
		closer(closeSetup)
		return nil, err
	}
	listener, err := server.Listen()
	if err != nil {
		closer(closeSetup)
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
			closer(closeSetup)
		})
	}, nil
}

func closer(closeSetup func()) {
	if closeSetup != nil {
		closeSetup()
	}
}

// pairingManager is the API path of `bot bind`: the session it runs resolves
// the bot's settings against the current config snapshot, so a reload reaches
// it too, and starts the bot when WhatsApp accepts the scan (ADR 0025).
func pairingManager(
	source *config.Source, db *sqlite.DB, devices *whatsapp.Devices,
	onPaired func(context.Context, string, string) error, logger *slog.Logger,
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
			options, err := whatsappOptions(bot)
			if err != nil {
				return whatsapp.PairOptions{}, err
			}
			return whatsapp.PairOptions{
				Scope:          scope,
				Bot:            botName,
				Devices:        devices,
				Bindings:       db.Bindings(),
				Peers:          db.Peers(),
				Chats:          db.Chats(),
				Messages:       db.Messages(),
				History:        options.History,
				FullHistory:    fullHistory,
				Directory:      db.Directory(),
				ImportContacts: options.ImportContacts,
				Outbox:         db.Outbox(),
				Log:            logger,
				DepsLogLevel:   botflota.ParseLogLevel(cfg.LogLevelDeps),
				DeviceName:     options.DeviceName,
			}, nil
		},
		OnPaired: func(ctx context.Context, scope, botName string) error {
			if onPaired == nil {
				return errors.New("fleet is not running")
			}
			return onPaired(ctx, scope, botName)
		},
		Log: logger,
	})
}
