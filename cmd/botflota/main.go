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

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/config"
)

func main() {
	os.Exit(dispatch(cliTree(), nil, os.Args[1:]))
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
// operations share. A nil logger keeps its output quiet; depsLevel is the
// configured log_level_deps.
func openDevices(
	ctx context.Context, db *sqlite.DB, log *slog.Logger, depsLevel string,
) (*whatsapp.Devices, error) {
	if log == nil {
		log = botflota.DiscardLogger()
	}
	return whatsapp.OpenDevices(ctx, db.DB, log, botflota.ParseLogLevel(depsLevel))
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
