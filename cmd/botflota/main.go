// Command botflota is the command-line interface to the Botflota fleet runner.
package main

import (
	"context"
	"errors"
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
	"github.com/felixzsh/botflota/instance"
	"github.com/felixzsh/botflota/spec"
)

// version is injected at release time with
// `-ldflags "-X main.version=..."`.
var version = "dev"

func main() {
	os.Exit(dispatch(cliTree(), nil, os.Args[1:]))
}

func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "`FILE` path to the config file (default: XDG discovery)")
}

const systemConfigDir = "/etc"

func discoverConfig() (string, error) {
	if path := os.Getenv("BOTFLOTA_CONFIG"); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("BOTFLOTA_CONFIG=%s: %w", path, err)
		}
		return path, nil
	}
	return firstExisting(configSearchPaths())
}

func configSearchPaths() []string {
	var paths []string
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "botflota", "config.yml"))
	}
	return append(paths, filepath.Join(systemConfigDir, "botflota", "config.yml"))
}

func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "botflota", "config.yml"), nil
}

func firstExisting(paths []string) (string, error) {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no config found; tried %s", strings.Join(paths, ", "))
}

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

// openDevices: a nil logger keeps the whatsmeow store quiet.
func openDevices(
	ctx context.Context, db *sqlite.DB, log *slog.Logger, depsLevel string,
) (*whatsapp.Devices, error) {
	if log == nil {
		log = botflota.DiscardLogger()
	}
	return whatsapp.OpenDevices(ctx, db.DB, log, botflota.ParseLogLevel(depsLevel))
}

// lockInstance takes the exclusive ownership that keeps a local write command
// out of a running fleet (ADR 0039). It does not look at the API port: the
// owner process is the one holding the lock.
func lockInstance(cfg *config.Config) (*instance.Lock, error) {
	dir, err := cfg.DataDirPath()
	if err != nil {
		return nil, err
	}
	return instance.Acquire(dir)
}

// releaseInstance releases the ownership taken by lockInstance.
func releaseInstance(lock *instance.Lock, report func(error)) {
	if err := lock.Release(); err != nil {
		report(err)
	}
}

// reportOwned explains a refused local command: the instance is taken by
// `fleet run` or another local command, and their API is the way in.
func reportOwned(err error, before string) {
	if errors.Is(err, instance.ErrOwned) {
		fmt.Fprintf(os.Stderr,
			"another botflota process owns this instance; stop fleet run before %s\n",
			before)
		return
	}
	fmt.Fprintln(os.Stderr, err)
}

// historyEnabled: an absent history_sync switch means on (ADR 0035).
func historyEnabled(settings *spec.BotSettings) bool {
	return settings == nil || settings.HistorySync == nil || *settings.HistorySync
}

// contactsEnabled: an absent import_contacts switch means off, because the
// address book may hold people the account never wrote to (ADR 0037).
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
