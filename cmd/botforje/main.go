// Command botforje is the BotForje CLI. Phase 1 implements validate and the
// schema generator; the rest of the commands arrive in later phases.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/felixzsh/botforje/internal/config"
	"github.com/felixzsh/botforje/internal/domain"
	"github.com/felixzsh/botforje/internal/fleet"
	"github.com/felixzsh/botforje/internal/logging"
	"github.com/felixzsh/botforje/internal/sqlite"
	"github.com/felixzsh/botforje/internal/whatsapp"
)

func main() {
	if len(os.Args) < 2 {
		print_help()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "validate":
		os.Exit(runValidate(os.Args[2:]))
	case "schema":
		os.Exit(runSchema(os.Args[2:]))
	case "auth":
		os.Exit(runAuth(os.Args[2:]))
	case "status":
		os.Exit(runStatus(os.Args[2:]))
	case "daemon":
		os.Exit(runDaemon(os.Args[2:]))
	case "help", "-h", "--help":
		print_help()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		print_help()
		os.Exit(2)
	}
}

func print_help() {
	fmt.Fprint(os.Stderr, `botforje <command>

Commands:
  validate   validate a config file (--config, --json)
  schema     print the generated config JSON Schema (-o file)
  auth       pair a bot by scanning a QR code (auth <bot> [--scope name])
  status     show each configured bot's pairing state
  daemon     run every configured bot until interrupted
`)
}

func runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.yml (default: XDG discovery)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path := *configPath
	if path == "" {
		found, err := discoverConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		path = found
	}

	cfg, err := (config.Loader{}).Load(path)
	if err != nil {
		if *asJSON {
			printJSON([]config.ValidationError{{File: path, Reason: err.Error()}})
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}

	if errs := config.Validate(cfg); len(errs) > 0 {
		if *asJSON {
			printJSON(errs)
		} else {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, e.Error())
			}
			fmt.Fprintf(os.Stderr, "\n%d problem(s) in %s\n", len(errs), path)
		}
		return 1
	}

	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": true, "config": path})
	} else {
		fmt.Printf("OK: %s\n", path)
	}
	return 0
}

func runSchema(args []string) int {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	out := fs.String("o", "", "output file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	data, err := config.JSONSchema()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *out == "" {
		_, _ = os.Stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("wrote %s\n", *out)
	return 0
}

func printJSON(errs []config.ValidationError) {
	type item struct {
		File   string `json:"file,omitempty"`
		Entity string `json:"entity,omitempty"`
		Reason string `json:"reason"`
	}
	items := make([]item, 0, len(errs))
	for _, e := range errs {
		items = append(items, item{File: e.File, Entity: e.Entity, Reason: e.Reason})
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": false, "errors": items})
}

func discoverConfig() (string, error) {
	if path := os.Getenv("BOTFORJE_CONFIG"); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("BOTFORJE_CONFIG=%s: %w", path, err)
		}
		return path, nil
	}
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "botforje", "config.yml"))
	}
	candidates = append(candidates, "/etc/botforje/config.yml")
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no config found; tried %s (use --config)",
		strings.Join(candidates, ", "))
}

func runAuth(args []string) int {
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.yml (default: XDG discovery)")
	scope := fs.String("scope", "", "scope that owns the bot (needed if the name repeats)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: botforje auth <bot> [--scope name] [--config path]")
		return 2
	}
	name := fs.Arg(0)

	cfg, code := loadConfig(*configPath)
	if code != 0 {
		return code
	}
	scopeName, _, err := findBot(cfg, name, *scope)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	db, err := openDatabase(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	jid, err := whatsapp.Pair(context.Background(), whatsapp.PairOptions{
		Scope:    scopeName,
		Bot:      name,
		DB:       db.DB,
		Accounts: db.Accounts(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("bot %q (scope %q) is paired as %s\n", name, scopeName, jid)
	return 0
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.yml (default: XDG discovery)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, code := loadConfig(*configPath)
	if code != 0 {
		return code
	}
	db, err := openDatabase(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	for _, scopeName := range sortedScopeNames(cfg) {
		for _, botName := range sortedBotNames(cfg.Scopes[scopeName]) {
			account, err := db.Accounts().Get(context.Background(), scopeName, botName)
			switch {
			case errors.Is(err, domain.ErrNotFound):
				fmt.Printf("%-24s scope=%-12s not paired\n", botName, scopeName)
			case err != nil:
				fmt.Fprintln(os.Stderr, err)
				return 1
			default:
				fmt.Printf("%-24s scope=%-12s paired as %s\n",
					botName, scopeName, account.JID)
			}
		}
	}
	return 0
}

func runDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.yml (default: XDG discovery)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, code := loadConfig(*configPath)
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

	runner, err := fleet.New(cfg, fleet.Options{
		Store:          db.Sessions(),
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
	logger.Info("daemon starting", "bots", len(statuses))
	for _, status := range statuses {
		logger.Info("bot configured", "scope", status.Scope, "bot", status.Bot)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		logger.Info("shutdown requested")
	}()
	if err := runner.Run(ctx); err != nil {
		return 1
	}
	logger.Info("daemon stopped")
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

// loadConfig discovers and validates the config, returning an exit code on
// failure.
func loadConfig(path string) (*config.Config, int) {
	if path == "" {
		found, err := discoverConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil, 2
		}
		path = found
	}
	cfg, err := (config.Loader{}).Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, 1
	}
	if errs := config.Validate(cfg); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e.Error())
		}
		return nil, 1
	}
	return cfg, 0
}

func openDatabase(cfg *config.Config) (*sqlite.DB, error) {
	dir, err := cfg.DataDirPath()
	if err != nil {
		return nil, err
	}
	return sqlite.Open(filepath.Join(dir, "botforje.db"))
}

func findBot(cfg *config.Config, name, scope string) (string, config.Bot, error) {
	if scope != "" {
		s, ok := cfg.Scopes[scope]
		if !ok {
			return "", config.Bot{}, fmt.Errorf("unknown scope %q", scope)
		}
		bot, ok := s.Bots[name]
		if !ok {
			return "", config.Bot{}, fmt.Errorf("bot %q not found in scope %q", name, scope)
		}
		return scope, bot, nil
	}
	var matches []string
	for _, scopeName := range sortedScopeNames(cfg) {
		if _, ok := cfg.Scopes[scopeName].Bots[name]; ok {
			matches = append(matches, scopeName)
		}
	}
	switch len(matches) {
	case 0:
		return "", config.Bot{}, fmt.Errorf("bot %q not found", name)
	case 1:
		return matches[0], cfg.Scopes[matches[0]].Bots[name], nil
	default:
		return "", config.Bot{}, fmt.Errorf("bot %q exists in scopes %s; pass --scope",
			name, strings.Join(matches, ", "))
	}
}

func sortedScopeNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Scopes))
	for name := range cfg.Scopes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedBotNames(scope config.Scope) []string {
	names := make([]string, 0, len(scope.Bots))
	for name := range scope.Bots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
