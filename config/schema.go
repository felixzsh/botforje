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
)

// JSONSchema generates the editor schema from the Go types (ADR 0007). The
// output is written to config.schema.json and must not be hand-edited.
func JSONSchema() ([]byte, error) {
	reflector := &jsonschema.Reflector{
		Anonymous:                 true,
		AllowAdditionalProperties: false,
	}
	addGoComments(reflector)
	schema := reflector.Reflect(&Config{})
	schema.Version = "https://json-schema.org/draft/2020-12/schema"

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(schema); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// addGoComments loads the doc comments of this package and spec into the
// reflector. The library keys each comment by the module path joined with the
// walked directory, so a walk from anywhere but the module root produces keys
// the reflector never looks up and every description is silently lost. The
// walk here runs on the absolute directories the source file knows, and the
// keys are rewritten to the import paths afterwards, so generation works from
// any working directory. Without sources around (a trimmed release binary)
// the schema is generated without descriptions rather than failing.
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

// addPackageComments adds one package directory and remaps its comment keys
// from the walked path to the import path.
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

// moduleRoot returns the module path and its directory, found by walking up
// from this source file to go.mod.
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
