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

// Load reads the main file, merges its includes (recursively), and expands
// ${ENV_VAR}. It does not run semantic validation; call Validate for that.
func (l Loader) Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{origin: map[string]string{}, source: abs}
	state := &includeState{loaded: map[string]bool{abs: true}}
	if err := state.loadMain(abs, cfg); err != nil {
		return nil, err
	}
	if misses := expandValue(reflect.ValueOf(cfg).Elem(), l.lookup); len(misses) > 0 {
		sort.Strings(misses)
		return nil, fmt.Errorf("%s: missing environment variable(s): %s",
			path, strings.Join(misses, ", "))
	}
	return cfg, nil
}

// includeState tracks loaded files so diamond includes merge once and cycles
// terminate (ADR 0016).
type includeState struct {
	loaded map[string]bool
}

func (s *includeState) loadMain(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := decodeStrict(path, data, cfg); err != nil {
		return err
	}
	recordOrigins(cfg, path)
	return s.runIncludes(path, cfg.Include, cfg)
}

func (s *includeState) runIncludes(from string, includes []string, cfg *Config) error {
	dir := filepath.Dir(from)
	for _, inc := range includes {
		files, err := resolveInclude(dir, inc)
		if err != nil {
			return fmt.Errorf("%s: include %q: %w", from, inc, err)
		}
		if len(files) == 0 {
			return fmt.Errorf("%s: include %q matched no files", from, inc)
		}
		for _, file := range files {
			if err := s.loadFragment(file, cfg); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *includeState) loadFragment(path string, cfg *Config) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if s.loaded[abs] {
		return nil
	}
	s.loaded[abs] = true

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var frag Fragment
	if err := decodeStrict(path, data, &frag); err != nil {
		return err
	}
	if err := mergeScopes(cfg, path, frag.Scopes); err != nil {
		return err
	}
	return s.runIncludes(path, frag.Include, cfg)
}

func mergeScopes(cfg *Config, file string, scopes map[string]Scope) error {
	for _, scopeName := range sortedKeys(scopes) {
		src := scopes[scopeName]
		if cfg.Scopes == nil {
			cfg.Scopes = map[string]Scope{}
		}
		dst := cfg.Scopes[scopeName]
		if err := mergeLLMs(cfg, file, scopeName, &dst, src.LLMs); err != nil {
			return err
		}
		if err := mergeActions(cfg, file, scopeName, &dst, src.Actions); err != nil {
			return err
		}
		if err := mergeGraphs(cfg, file, scopeName, &dst, src.Graphs); err != nil {
			return err
		}
		if err := mergeBots(cfg, file, scopeName, &dst, src.Bots); err != nil {
			return err
		}
		cfg.Scopes[scopeName] = dst
	}
	return nil
}

func mergeLLMs(cfg *Config, file, scope string, dst *Scope, src map[string]LLM) error {
	for _, name := range sortedKeys(src) {
		if dst.LLMs == nil {
			dst.LLMs = map[string]LLM{}
		}
		if _, exists := dst.LLMs[name]; exists {
			return duplicateError(cfg, file, "llm", scope, name)
		}
		dst.LLMs[name] = src[name]
		cfg.origin[originKey("llm", scope, name)] = file
	}
	return nil
}

func mergeActions(cfg *Config, file, scope string, dst *Scope, src map[string]Action) error {
	for _, name := range sortedKeys(src) {
		if dst.Actions == nil {
			dst.Actions = map[string]Action{}
		}
		if _, exists := dst.Actions[name]; exists {
			return duplicateError(cfg, file, "action", scope, name)
		}
		dst.Actions[name] = src[name]
		cfg.origin[originKey("action", scope, name)] = file
	}
	return nil
}

func mergeGraphs(cfg *Config, file, scope string, dst *Scope, src map[string]Graph) error {
	for _, name := range sortedKeys(src) {
		if dst.Graphs == nil {
			dst.Graphs = map[string]Graph{}
		}
		if _, exists := dst.Graphs[name]; exists {
			return duplicateError(cfg, file, "graph", scope, name)
		}
		dst.Graphs[name] = src[name]
		cfg.origin[originKey("graph", scope, name)] = file
	}
	return nil
}

func mergeBots(cfg *Config, file, scope string, dst *Scope, src map[string]Bot) error {
	for _, name := range sortedKeys(src) {
		if dst.Bots == nil {
			dst.Bots = map[string]Bot{}
		}
		if _, exists := dst.Bots[name]; exists {
			return duplicateError(cfg, file, "bot", scope, name)
		}
		dst.Bots[name] = src[name]
		cfg.origin[originKey("bot", scope, name)] = file
	}
	return nil
}

func duplicateError(cfg *Config, file, kind, scope, name string) error {
	if prev := cfg.origin[originKey(kind, scope, name)]; prev != "" {
		return fmt.Errorf("%s: duplicate %s %q in scope %q (already defined in %s)",
			file, kind, name, scope, prev)
	}
	return fmt.Errorf("%s: duplicate %s %q in scope %q", file, kind, name, scope)
}

func recordOrigins(cfg *Config, path string) {
	for _, scopeName := range sortedKeys(cfg.Scopes) {
		scope := cfg.Scopes[scopeName]
		for name := range scope.LLMs {
			cfg.origin[originKey("llm", scopeName, name)] = path
		}
		for name := range scope.Actions {
			cfg.origin[originKey("action", scopeName, name)] = path
		}
		for name := range scope.Graphs {
			cfg.origin[originKey("graph", scopeName, name)] = path
		}
		for name := range scope.Bots {
			cfg.origin[originKey("bot", scopeName, name)] = path
		}
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

func originKey(kind, scope, name string) string {
	return kind + "/" + scope + "/" + name
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
