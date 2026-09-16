package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/invopop/jsonschema"
)

//go:generate go run ./cmd/botflota schema -o config.schema.json

// JSONSchema generates the editor schema from the Go types (ADR 0007). The
// output is written to config.schema.json and must not be hand-edited.
func JSONSchema() ([]byte, error) {
	reflector := &jsonschema.Reflector{
		Anonymous:                 true,
		AllowAdditionalProperties: false,
	}
	reflector.AddGoComments(modulePath(), packageRelPath())
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

// packageRelPath returns this package's directory relative to the working
// directory, which is what AddGoComments expects. Run regeneration from the
// repository root so the comments resolve.
func packageRelPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	rel, err := filepath.Rel(wd, filepath.Dir(file))
	if err != nil {
		return "."
	}
	return rel
}

// modulePath reads the module path from the nearest go.mod.
func modulePath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(file)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if rest, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
					return strings.TrimSpace(rest)
				}
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
