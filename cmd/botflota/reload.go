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

	"github.com/felixzsh/botflota/channels"
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
func reloadOutcome(path string, current *config.Config, catalog *channels.Catalog) (
	*config.Config, []string, error,
) {
	next, err := (config.Loader{}).Load(path)
	if err != nil {
		return nil, nil, err
	}
	if problems := config.Validate(next, catalog); len(problems) > 0 {
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
	ctx context.Context, path string, source, saved *config.Source,
	tree *config.Writer, runner *fleet.Fleet, catalog *channels.Catalog,
	logger *slog.Logger,
) {
	poll := &configPoll{
		path: path, source: source, saved: saved, tree: tree,
		runner: runner, catalog: catalog, logger: logger,
	}
	poll.stamp = filesStamp(source.Current().WatchPaths())
	ticker := time.NewTicker(configWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		poll.tick(ctx)
	}
}

// configPoll is one watcher's state between polls. Each tick holds the tree
// while it reads and activates, so it never loads a publication's files while
// they are being replaced.
type configPoll struct {
	path    string
	source  *config.Source
	saved   *config.Source
	tree    *config.Writer
	runner  *fleet.Fleet
	catalog *channels.Catalog
	logger  *slog.Logger

	stamp         string
	candidate     *config.Config
	pending       string
	lastLoadError string
	retryLoad     bool
}

func (p *configPoll) tick(ctx context.Context) {
	unlock, err := p.tree.Sync(ctx)
	if err != nil {
		// Shutting down; the next start recovers anything a publication left
		// unfinished.
		return
	}
	defer unlock()
	current := p.source.Current()
	stamp := filesStamp(watchPaths(current, p.candidate))
	changed := stamp != p.stamp
	if changed {
		p.stamp = stamp
	}
	if changed || (p.candidate == nil && p.retryLoad) {
		loaded, err := loadCandidate(p.path, p.catalog)
		if err != nil {
			p.candidate = nil
			p.retryLoad = true
			if err.Error() != p.lastLoadError {
				p.logger.Error("config reload failed; keeping the current config",
					"error", err)
				p.lastLoadError = err.Error()
			}
			return
		}
		p.candidate = loaded
		// The files are now the saved truth even if activation is still
		// pending, so readers see the document an editor must write against.
		p.saved.Replace(loaded)
		p.retryLoad = false
		p.lastLoadError = ""
	}
	if p.candidate == nil {
		return
	}
	plan := config.PlanReload(current, p.candidate)
	if len(plan.Scopes) == 0 && len(plan.Global) == 0 {
		p.source.Replace(config.WithScopes(current, p.candidate, nil))
		p.candidate = nil
		p.pending = ""
		return
	}
	applied, pending, err := p.runner.ApplyConfig(ctx, p.candidate, plan.Scopes, p.source)
	if err != nil {
		p.logger.Error("config reload failed; keeping the current config",
			"error", err)
		return
	}
	if len(applied) > 0 {
		p.logger.Info("config scopes activated", "scopes", applied)
	}
	key := ""
	if len(plan.Global) > 0 {
		key = fmt.Sprint("global:", plan.Global)
	}
	for _, scope := range config.SortedKeys(pending) {
		key += fmt.Sprint(";", scope, ":", pending[scope])
	}
	switch {
	case key == "":
		p.pending = ""
		p.candidate = nil
	case key != p.pending:
		p.logger.Warn("config changes saved but not active",
			"global", plan.Global, "scopes", pending)
		p.pending = key
	}
}

func loadCandidate(path string, catalog *channels.Catalog) (*config.Config, error) {
	next, err := (config.Loader{}).Load(path)
	if err != nil {
		return nil, err
	}
	if problems := config.Validate(next, catalog); len(problems) > 0 {
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
