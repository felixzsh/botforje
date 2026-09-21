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
	"github.com/felixzsh/botflota/spec"
)

// version is the build version, set with
// `-ldflags "-X main.version=..."` at release time.
var version = "dev"

func main() {
	os.Exit(dispatch(cliTree(), nil, os.Args[1:]))
}

// configFlag registers the common --config flag shared by every subcommand.
func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "`FILE` path to the config file (default: XDG discovery)")
}

// systemConfigDir is the last place discovery looks.
const systemConfigDir = "/etc"

// discoverConfig returns the first config file that exists: BOTFLOTA_CONFIG,
// the user config directory, then the system one. An explicit --config wins
// over all of them and the caller uses it directly.
func discoverConfig() (string, error) {
	if path := os.Getenv("BOTFLOTA_CONFIG"); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("BOTFLOTA_CONFIG=%s: %w", path, err)
		}
		return path, nil
	}
	return firstExisting(configSearchPaths())
}

// configSearchPaths lists where discovery looks, most specific first.
func configSearchPaths() []string {
	var paths []string
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "botflota", "config.yml"))
	}
	return append(paths, filepath.Join(systemConfigDir, "botflota", "config.yml"))
}

// defaultConfigPath is where `config init` writes without --config: the user
// config directory, the first place discovery checks.
func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "botflota", "config.yml"), nil
}

// firstExisting returns the first path that exists, or an error naming every
// candidate.
func firstExisting(paths []string) (string, error) {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no config found; tried %s", strings.Join(paths, ", "))
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

// historyEnabled interprets the per-bot history_sync switch; absent means on
// (ADR 0035).
func historyEnabled(settings *spec.BotSettings) bool {
	return settings == nil || settings.HistorySync == nil || *settings.HistorySync
}

// contactsEnabled interprets the per-bot import_contacts switch; absent means
// off, because the address book may hold people the account never wrote to
// (ADR 0037).
func contactsEnabled(settings *spec.BotSettings) bool {
	return settings != nil && settings.ImportContacts != nil && *settings.ImportContacts
}

func findBot(cfg *config.Config, name, scope string) (string, spec.Bot, error) {
	if scope != "" {
		s, ok := cfg.Scopes[scope]
		if !ok {
			return "", spec.Bot{}, fmt.Errorf("unknown scope %q", scope)
		}
		bot, ok := s.Bots[name]
		if !ok {
			return "", spec.Bot{}, fmt.Errorf("bot %q not found in scope %q", name, scope)
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
		return "", spec.Bot{}, fmt.Errorf("bot %q not found", name)
	case 1:
		return matches[0], cfg.Scopes[matches[0]].Bots[name], nil
	default:
		return "", spec.Bot{}, fmt.Errorf("bot %q exists in scopes %s",
			name, strings.Join(matches, ", "))
	}
}
