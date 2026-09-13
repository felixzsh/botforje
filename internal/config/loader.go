package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Loader reads configuration files.
type Loader struct {
	// LookupEnv resolves environment variables. Defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

func (l Loader) lookup(name string) (string, bool) {
	if l.LookupEnv != nil {
		return l.LookupEnv(name)
	}
	return os.LookupEnv(name)
}

// Load reads the main file, merges its includes, and expands ${ENV_VAR}.
// It does not run semantic validation; call Validate for that.
func (l Loader) Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg := &Config{origin: map[string]string{}}
	if err := decodeStrict(path, data, cfg); err != nil {
		return nil, err
	}
	recordOrigins(cfg, path, cfg.Actions, cfg.Graphs, cfg.Bots)

	if err := l.loadIncludes(path, cfg); err != nil {
		return nil, err
	}

	if misses := expandValue(reflect.ValueOf(cfg).Elem(), l.lookup); len(misses) > 0 {
		sort.Strings(misses)
		return nil, fmt.Errorf("%s: missing environment variable(s): %s",
			path, strings.Join(misses, ", "))
	}
	return cfg, nil
}

func (l Loader) loadIncludes(mainPath string, cfg *Config) error {
	for _, inc := range cfg.Include {
		files, err := resolveInclude(filepath.Dir(mainPath), inc)
		if err != nil {
			return fmt.Errorf("%s: include %q: %w", mainPath, inc, err)
		}
		if len(files) == 0 {
			return fmt.Errorf("%s: include %q matched no files", mainPath, inc)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			var frag Fragment
			if err := decodeStrict(file, data, &frag); err != nil {
				return err
			}
			if err := mergeFragment(cfg, file, &frag); err != nil {
				return err
			}
		}
	}
	return nil
}

func mergeFragment(cfg *Config, file string, frag *Fragment) error {
	for _, name := range sortedKeys(frag.Actions) {
		if cfg.Actions == nil {
			cfg.Actions = map[string]Action{}
		}
		action := frag.Actions[name]
		if _, exists := cfg.Actions[name]; exists {
			return duplicateError(cfg, file, "action", action.Scope, name)
		}
		cfg.Actions[name] = action
		cfg.origin[originKey("action", action.Scope, name)] = file
	}
	for _, name := range sortedKeys(frag.Graphs) {
		if cfg.Graphs == nil {
			cfg.Graphs = map[string]Graph{}
		}
		graph := frag.Graphs[name]
		if _, exists := cfg.Graphs[name]; exists {
			return duplicateError(cfg, file, "graph", graph.Scope, name)
		}
		cfg.Graphs[name] = graph
		cfg.origin[originKey("graph", graph.Scope, name)] = file
	}
	for _, name := range sortedKeys(frag.Bots) {
		if cfg.Bots == nil {
			cfg.Bots = map[string]Bot{}
		}
		bot := frag.Bots[name]
		if _, exists := cfg.Bots[name]; exists {
			return duplicateError(cfg, file, "bot", bot.Scope, name)
		}
		cfg.Bots[name] = bot
		cfg.origin[originKey("bot", bot.Scope, name)] = file
	}
	return nil
}

func duplicateError(cfg *Config, file, kind, scope, name string) error {
	key := originKey(kind, scope, name)
	if prev := cfg.origin[key]; prev != "" {
		return fmt.Errorf("%s: duplicate %s %q in scope %q (already defined in %s)",
			file, kind, name, scopeOf(scope), prev)
	}
	return fmt.Errorf("%s: duplicate %s %q in scope %q", file, kind, name, scopeOf(scope))
}

func recordOrigins(cfg *Config, path string, actions map[string]Action, graphs map[string]Graph, bots map[string]Bot) {
	for name := range actions {
		cfg.origin[originKey("action", actions[name].Scope, name)] = path
	}
	for name := range graphs {
		cfg.origin[originKey("graph", graphs[name].Scope, name)] = path
	}
	for name := range bots {
		cfg.origin[originKey("bot", bots[name].Scope, name)] = path
	}
}

func decodeStrict(path string, data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s: empty document", path)
		}
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func resolveInclude(dir, entry string) ([]string, error) {
	pattern := entry
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(dir, entry)
	}
	if strings.ContainsAny(entry, "*?[") {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		return matches, nil
	}
	info, err := os.Stat(pattern)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{pattern}, nil
	}
	entries, err := os.ReadDir(pattern)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yml", ".yaml":
			files = append(files, filepath.Join(pattern, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func scopeOf(scope string) string {
	if scope == "" {
		return "default"
	}
	return scope
}

func originKey(kind, scope, name string) string {
	return kind + "/" + scopeOf(scope) + "/" + name
}

func (c *Config) originOf(kind, scope, name string) string {
	if c == nil || c.origin == nil {
		return ""
	}
	return c.origin[originKey(kind, scope, name)]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
