// Command botflota is the Botflota CLI. Phase 1 implements validate and the
// schema generator; the rest of the commands arrive in later phases.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/felixzsh/botflota/internal/config"
	"github.com/felixzsh/botflota/internal/logging"
	"github.com/felixzsh/botflota/internal/sqlite"
	"github.com/felixzsh/botflota/internal/whatsapp"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "config":
		os.Exit(runConfig(args[1:]))
	case "bot":
		os.Exit(runBot(args[1:]))
	case "fleet":
		os.Exit(runFleetGroup(args[1:]))
	case "help", "-h", "--help":
		printHelp(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		printHelp(os.Stderr)
		os.Exit(2)
	}
}

func runConfig(args []string) int {
	if len(args) == 0 {
		printHelp(os.Stderr, "config")
		return 2
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:])
	case "schema":
		return runSchema(args[1:])
	case "help", "-h", "--help":
		printHelp(os.Stdout, "config")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand %q\n\n", args[0])
		printHelp(os.Stderr, "config")
		return 2
	}
}

func runBot(args []string) int {
	if len(args) == 0 {
		printHelp(os.Stderr, "bot")
		return 2
	}
	switch args[0] {
	case "list":
		return runBotList(args[1:])
	case "pair":
		return runPair(args[1:])
	case "unpair":
		return runUnpair(args[1:])
	case "help", "-h", "--help":
		printHelp(os.Stdout, "bot")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown bot subcommand %q\n\n", args[0])
		printHelp(os.Stderr, "bot")
		return 2
	}
}

func runFleetGroup(args []string) int {
	if len(args) == 0 {
		printHelp(os.Stderr, "fleet")
		return 2
	}
	switch args[0] {
	case "run":
		return runFleet(args[1:])
	case "help", "-h", "--help":
		printHelp(os.Stdout, "fleet")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown fleet subcommand %q\n\n", args[0])
		printHelp(os.Stderr, "fleet")
		return 2
	}
}

// configFlag registers the common --config flag shared by every subcommand.
func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "`FILE` path to the config file (default: XDG discovery)")
}

func discoverConfig() (string, error) {
	if path := os.Getenv("BOTFLOTA_CONFIG"); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("BOTFLOTA_CONFIG=%s: %w", path, err)
		}
		return path, nil
	}
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "botflota", "config.yml"))
	}
	candidates = append(candidates, "/etc/botflota/config.yml")
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no config found; tried %s",
		strings.Join(candidates, ", "))
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
	return sqlite.Open(filepath.Join(dir, "botflota.db"))
}

// openDevices opens the whatsmeow device store that a command's pairing
// operations share. A nil logger keeps its output quiet.
func openDevices(
	ctx context.Context, db *sqlite.DB, log *slog.Logger,
) (*whatsapp.Devices, error) {
	if log == nil {
		log = logging.Discard()
	}
	return whatsapp.OpenDevices(ctx, db.DB, log)
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
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
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
		return "", config.Bot{}, fmt.Errorf("bot %q exists in scopes %s",
			name, strings.Join(matches, ", "))
	}
}
