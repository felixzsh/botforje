// Package spec holds the declarative objects Botflota understands: scopes,
// actions, graphs, bots, and model profiles (ADR 0034). YAML and JSON tags
// are metadata for decoding; the engine and the future database read the
// fields directly.
package spec

import (
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
	"go.yaml.in/yaml/v3"
)

// Text is a message body: one string, or a list of variants picked at random.
type Text []string

// UnmarshalYAML accepts a scalar string or a sequence of strings.
func (t *Text) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		*t = Text{s}
	case yaml.SequenceNode:
		var ss []string
		if err := value.Decode(&ss); err != nil {
			return err
		}
		*t = Text(ss)
	default:
		return fmt.Errorf("body must be a string or a list of strings")
	}
	return nil
}

// MarshalYAML renders a single variant as a scalar and several as a list.
func (t Text) MarshalYAML() (any, error) {
	if len(t) == 1 {
		return t[0], nil
	}
	return []string(t), nil
}

// JSONSchema describes Text as a string or an array of strings.
func (Text) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		OneOf: []*jsonschema.Schema{
			{Type: "string"},
			{Type: "array", Items: &jsonschema.Schema{Type: "string"}},
		},
	}
}

// Secret is a value that must not leak into logs, errors, or the API
// (ADR 0014). Use Reveal to read it.
type Secret string

// Reveal returns the real value. Call it only where the secret is consumed.
func (s Secret) Reveal() string { return string(s) }

// String masks the value.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "***"
}

// GoString masks the value under %#v.
func (s Secret) GoString() string { return s.String() }

// MarshalJSON emits the masked value.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// MarshalYAML emits the masked value.
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }

// JSONSchema describes Secret as a string.
func (Secret) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string"}
}
