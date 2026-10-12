package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	gopath "path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/felixzsh/botflota/channels"
)

// JSONSchema generates the editor schema from the Go types and the installed
// channel definitions (ADR 0007, ADR 0054). The output is written to
// config.schema.json and must not be hand-edited.
func JSONSchema(catalog *channels.Catalog) ([]byte, error) {
	reflector := &jsonschema.Reflector{
		Anonymous:                 true,
		AllowAdditionalProperties: false,
	}
	addGoComments(reflector)
	schema := reflector.Reflect(&Config{})
	schema.Version = "https://json-schema.org/draft/2020-12/schema"

	root, err := schemaMap(schema)
	if err != nil {
		return nil, err
	}
	if err := applyChannelCatalog(root, catalog); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func schemaMap(schema *jsonschema.Schema) (map[string]any, error) {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		return nil, err
	}
	return root, nil
}

// applyChannelCatalog replaces the channel selector's schema with one branch
// per installed provider and account mode, so an editor validates options
// against the implementation they belong to and rejects a selection that is
// not installed (ADR 0054).
func applyChannelCatalog(root map[string]any, catalog *channels.Catalog) error {
	defs, _ := root["$defs"].(map[string]any)
	if defs == nil {
		return errors.New("config: generated schema has no $defs")
	}
	var branches []any
	for _, key := range catalog.Keys() {
		def, ok := catalog.Lookup(key.Provider, key.Mode)
		if !ok {
			continue
		}
		options := map[string]any{"type": "object"}
		if def.OptionsSchema != nil {
			built, err := schemaMap(def.OptionsSchema())
			if err != nil {
				return fmt.Errorf("config: channel %s/%s options schema: %w",
					key.Provider, key.Mode, err)
			}
			if built != nil {
				options = built
			}
		}
		delete(options, "$schema")
		branches = append(branches, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"provider": map[string]any{"const": key.Provider},
				"mode":     map[string]any{"const": key.Mode},
				"options":  options,
			},
			"required":             []any{"provider", "mode"},
			"additionalProperties": false,
		})
	}
	if len(branches) == 0 {
		return errors.New("config: no channel definition is installed")
	}
	defs["Channel"] = map[string]any{"oneOf": branches}
	return nil
}

// addGoComments loads the doc comments of this package and spec. The
// reflector keys them by the walked path, so the walk runs on the absolute
// source directories and the keys are rewritten to import paths afterwards;
// without sources (a trimmed binary) the schema keeps working without
// descriptions.
func addGoComments(reflector *jsonschema.Reflector) {
	module, root, err := moduleRoot()
	if err != nil {
		return
	}
	dir := sourceDir()
	if dir == "" {
		return
	}
	for _, pkgDir := range []string{dir, filepath.Join(dir, "..", "spec")} {
		addPackageComments(reflector, module, root, pkgDir)
	}
}

func addPackageComments(
	reflector *jsonschema.Reflector, module, root, dir string,
) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return
	}
	prefix := gopath.Join(module, dir)
	if err := reflector.AddGoComments(module, dir); err != nil {
		return
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return
	}
	importPath := module + "/" + filepath.ToSlash(rel)
	for key, value := range reflector.CommentMap {
		suffix, ok := strings.CutPrefix(key, prefix)
		if !ok || !strings.HasPrefix(suffix, ".") {
			continue
		}
		delete(reflector.CommentMap, key)
		reflector.CommentMap[importPath+suffix] = value
	}
}

// sourceDir is the directory of this source file, or empty under -trimpath,
// where the compile-time path no longer exists on disk.
func sourceDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Dir(file)
}

func moduleRoot() (path, dir string, err error) {
	source := sourceDir()
	if source == "" {
		return "", "", errors.New("config: cannot locate the package sources")
	}
	for current := source; ; {
		data, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if rest, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
					return strings.TrimSpace(rest), current, nil
				}
			}
			return "", "", fmt.Errorf("config: no module line in %s/go.mod", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", errors.New("config: no go.mod above the package")
		}
		current = parent
	}
}
