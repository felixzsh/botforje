package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
)

const configWatchInterval = time.Second

// configPath resolves the file watchConfig reloads, the way loadConfig finds
// it.
func configPath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return discoverConfig()
}

// reloadOutcome loads path and says what a running fleet should do with it:
// an error keeps the current config, reasons mean restart-required, and a
// next config is one that applies live (ADR 0009).
func reloadOutcome(path string, current *config.Config) (
	*config.Config, []string, error,
) {
	next, err := (config.Loader{}).Load(path)
	if err != nil {
		return nil, nil, err
	}
	if problems := config.Validate(next); len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		return nil, nil, errors.New(strings.Join(messages, "; "))
	}
	if reasons := config.RequiresRestart(current, next); len(reasons) > 0 {
		return nil, reasons, nil
	}
	return next, nil, nil
}

// watchConfig polls the config files and applies a reload that validates;
// mtime polling keeps the watcher dependency-free (ADR 0009).
func watchConfig(
	ctx context.Context, path string, source *config.Source,
	runner *fleet.Fleet, logger *slog.Logger,
) {
	last := filesStamp(source.Current().Files())
	ticker := time.NewTicker(configWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		stamp := filesStamp(source.Current().Files())
		if stamp == last {
			continue
		}
		// Recording before applying keeps one edit to one log entry, even
		// when the reload fails or asks for a restart.
		last = stamp
		next, reasons, err := reloadOutcome(path, source.Current())
		switch {
		case err != nil:
			logger.Error("config reload failed; keeping the current config",
				"error", err)
		case len(reasons) > 0:
			logger.Warn("config saved, but a restart is required",
				"reasons", reasons)
		default:
			// The fleet adopts first and the snapshot publishes to readers
			// afterwards, so a reader never sees a config the fleet has not
			// taken yet (ADR 0009).
			runner.ApplyConfig(next)
			source.Replace(next)
			logger.Info("config reloaded")
		}
	}
}

// filesStamp fingerprints the watched files by modification time and size.
func filesStamp(files []string) string {
	var stamp strings.Builder
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			fmt.Fprintf(&stamp, "%s:missing;", file)
			continue
		}
		fmt.Fprintf(&stamp, "%s:%d:%d;",
			file, info.ModTime().UnixNano(), info.Size())
	}
	return stamp.String()
}
