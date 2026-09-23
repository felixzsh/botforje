package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota/spec"
)

type Loader struct {
	// LookupEnv resolves environment variables; nil means os.LookupEnv.
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
	cfg.files = state.paths()
	cfg.watchPaths = slices.Clone(cfg.files)
	cfg.watchPaths = append(cfg.watchPaths, state.watchDirs...)
	sort.Strings(cfg.watchPaths)
	cfg.watchPaths = slices.Compact(cfg.watchPaths)
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
	loaded    map[string]bool
	watchDirs []string
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
		s.watchDirs = append(s.watchDirs, includeWatchDir(dir, inc))
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

func includeWatchDir(from, entry string) string {
	pattern := entry
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(from, entry)
	}
	if wildcard := strings.IndexAny(pattern, "*?["); wildcard >= 0 {
		pattern = pattern[:wildcard]
		return filepath.Clean(filepath.Dir(pattern))
	}
	if info, err := os.Stat(pattern); err == nil && info.IsDir() {
		return filepath.Clean(pattern)
	}
	return filepath.Clean(filepath.Dir(pattern))
}

func (s *includeState) paths() []string {
	paths := make([]string, 0, len(s.loaded))
	for path := range s.loaded {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
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
	var frag fragment
	if err := decodeStrict(path, data, &frag); err != nil {
		return err
	}
	if err := mergeScopes(cfg, path, frag.Scopes); err != nil {
		return err
	}
	return s.runIncludes(path, frag.Include, cfg)
}

func mergeScopes(cfg *Config, file string, scopes map[string]spec.Scope) error {
	for _, scopeName := range SortedKeys(scopes) {
		src := scopes[scopeName]
		if cfg.Scopes == nil {
			cfg.Scopes = map[string]spec.Scope{}
		}
		dst := cfg.Scopes[scopeName]
		var err error
		if dst.LLMs, err = mergeEntities(cfg, file, "llm", scopeName,
			dst.LLMs, src.LLMs); err != nil {
			return err
		}
		if dst.Actions, err = mergeEntities(cfg, file, "action", scopeName,
			dst.Actions, src.Actions); err != nil {
			return err
		}
		if dst.Graphs, err = mergeEntities(cfg, file, "graph", scopeName,
			dst.Graphs, src.Graphs); err != nil {
			return err
		}
		if dst.Bots, err = mergeEntities(cfg, file, "bot", scopeName,
			dst.Bots, src.Bots); err != nil {
			return err
		}
		cfg.Scopes[scopeName] = dst
	}
	return nil
}

// mergeEntities copies one entity kind of an included file into a scope,
// rejecting a duplicate (scope, kind, name) and recording which file defined
// it (ADR 0016).
func mergeEntities[V any](
	cfg *Config, file, kind, scope string, dst, src map[string]V,
) (map[string]V, error) {
	if dst == nil && len(src) > 0 {
		dst = map[string]V{}
	}
	for _, name := range SortedKeys(src) {
		if _, exists := dst[name]; exists {
			return nil, duplicateError(cfg, file, kind, scope, name)
		}
		dst[name] = src[name]
		cfg.origin[originKey(kind, scope, name)] = file
	}
	return dst, nil
}

func duplicateError(cfg *Config, file, kind, scope, name string) error {
	if prev := cfg.origin[originKey(kind, scope, name)]; prev != "" {
		return fmt.Errorf("%s: duplicate %s %q in scope %q (already defined in %s)",
			file, kind, name, scope, prev)
	}
	return fmt.Errorf("%s: duplicate %s %q in scope %q", file, kind, name, scope)
}

func recordOrigins(cfg *Config, path string) {
	for _, scopeName := range SortedKeys(cfg.Scopes) {
		scope := cfg.Scopes[scopeName]
		recordOrigin(cfg, path, "llm", scopeName, scope.LLMs)
		recordOrigin(cfg, path, "action", scopeName, scope.Actions)
		recordOrigin(cfg, path, "graph", scopeName, scope.Graphs)
		recordOrigin(cfg, path, "bot", scopeName, scope.Bots)
	}
}

func recordOrigin[V any](cfg *Config, file, kind, scope string, names map[string]V) {
	for name := range names {
		cfg.origin[originKey(kind, scope, name)] = file
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

// SortedKeys returns a map's keys in sorted order, so validation output stays
// deterministic.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
