package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
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
	plan := config.PlanReload(current, next)
	reasons := append([]string(nil), plan.Global...)
	for _, scope := range plan.Scopes {
		reasons = append(reasons, plan.ScopeReasons[scope]...)
	}
	if len(reasons) > 0 {
		return nil, reasons, nil
	}
	return next, reasons, nil
}

// watchConfig polls the config files and applies a reload that validates;
// mtime polling keeps the watcher dependency-free (ADR 0009).
func watchConfig(
	ctx context.Context, path string, source *config.Source,
	runner *fleet.Fleet, logger *slog.Logger,
) {
	last := filesStamp(source.Current().WatchPaths())
	var candidate *config.Config
	var lastPending string
	var lastLoadError string
	retryLoad := false
	ticker := time.NewTicker(configWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		current := source.Current()
		stamp := filesStamp(watchPaths(current, candidate))
		changed := stamp != last
		if changed {
			last = stamp
		}
		if changed || (candidate == nil && retryLoad) {
			loaded, err := loadCandidate(path)
			if err != nil {
				candidate = nil
				retryLoad = true
				if err.Error() != lastLoadError {
					logger.Error("config reload failed; keeping the current config",
						"error", err)
					lastLoadError = err.Error()
				}
				continue
			}
			candidate = loaded
			retryLoad = false
			lastLoadError = ""
		}
		if candidate == nil {
			continue
		}
		plan := config.PlanReload(current, candidate)
		if len(plan.Scopes) == 0 && len(plan.Global) == 0 {
			source.Replace(config.WithScopes(current, candidate, nil))
			candidate = nil
			lastPending = ""
			continue
		}
		applied, pending, err := runner.ApplyConfig(ctx, candidate, plan.Scopes, source)
		if err != nil {
			logger.Error("config reload failed; keeping the current config",
				"error", err)
			continue
		}
		if len(applied) > 0 {
			current = config.WithScopes(current, candidate, applied)
			logger.Info("config scopes activated", "scopes", applied)
		}
		pendingKey := ""
		if len(plan.Global) > 0 {
			pendingKey = fmt.Sprint("global:", plan.Global)
		}
		for _, scope := range config.SortedKeys(pending) {
			pendingKey += fmt.Sprint(";", scope, ":", pending[scope])
		}
		if pendingKey != "" {
			if pendingKey != lastPending {
				logger.Warn("config changes saved but not active",
					"global", plan.Global, "scopes", pending)
			}
			lastPending = pendingKey
		} else {
			lastPending = ""
			candidate = nil
		}
	}
}

func loadCandidate(path string) (*config.Config, error) {
	next, err := (config.Loader{}).Load(path)
	if err != nil {
		return nil, err
	}
	if problems := config.Validate(next); len(problems) > 0 {
		messages := make([]string, 0, len(problems))
		for _, problem := range problems {
			messages = append(messages, problem.Error())
		}
		return nil, errors.New(strings.Join(messages, "; "))
	}
	return next, nil
}

func watchPaths(current, candidate *config.Config) []string {
	paths := append([]string(nil), current.WatchPaths()...)
	if candidate != nil {
		paths = append(paths, candidate.WatchPaths()...)
	}
	sort.Strings(paths)
	return slices.Compact(paths)
}

// filesStamp fingerprints file contents and directory metadata. Directory
// stamps notice new members of a directory or glob include.
func filesStamp(paths []string) string {
	var stamp strings.Builder
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(&stamp, "%s:missing;", path)
			continue
		}
		if info.IsDir() {
			fmt.Fprintf(&stamp, "%s:dir:%d:%d;", path,
				info.ModTime().UnixNano(), info.Size())
			entries, err := os.ReadDir(path)
			if err != nil {
				fmt.Fprintf(&stamp, "%s:unreadable:%v;", path, err)
				continue
			}
			for _, entry := range entries {
				child := filepath.Join(path, entry.Name())
				childInfo, err := entry.Info()
				if err != nil {
					fmt.Fprintf(&stamp, "%s:entry:%v;", child, err)
					continue
				}
				fmt.Fprintf(&stamp, "%s:%d:%d:%t;", child,
					childInfo.Size(), childInfo.ModTime().UnixNano(), childInfo.IsDir())
			}
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(&stamp, "%s:unreadable:%v;", path, err)
			continue
		}
		fmt.Fprintf(&stamp, "%s:%x;", filepath.Clean(path), sha256.Sum256(data))
	}
	return stamp.String()
}
