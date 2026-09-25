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

// UnmarshalJSON accepts one body or a list of variants, the same shapes
// UnmarshalYAML takes, so a document an API read can be sent back.
func (t *Text) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*t = Text{single}
		return nil
	}
	var several []string
	if err := json.Unmarshal(data, &several); err != nil {
		return fmt.Errorf("body must be a string or a list of strings")
	}
	*t = several
	return nil
}

// Secret is a value that must not leak into logs, errors, or the API
// (ADR 0014). It keeps the string as written, so serialization shows the
// `${ENV_VAR}` reference when the config used one and a mask when it did not.
// Use Reveal to read the resolved value.
type Secret struct {
	ref   string // the string as written when the loader resolved a reference
	value string // the resolved value, or the literal text
}

// NewSecret returns a secret with a literal value and no environment
// reference.
func NewSecret(value string) Secret { return Secret{value: value} }

// NewSecretRef returns a secret written as ref (a `${ENV_VAR}` reference, or a
// string embedding one) whose resolved value is value. The loader builds these
// during interpolation; Raw keeps the placeholder for serialization.
func NewSecretRef(ref, value string) Secret { return Secret{ref: ref, value: value} }

// Raw returns the string as written: the reference when the config used one,
// otherwise the value itself.
func (s Secret) Raw() string {
	if s.ref != "" {
		return s.ref
	}
	return s.value
}

// HasRef reports whether the loader resolved a `${...}` reference for this
// secret.
func (s Secret) HasRef() bool { return s.ref != "" }

// Reveal returns the real value. Call it only where the secret is consumed.
func (s Secret) Reveal() string { return s.value }

// String shows the reference when there was one and a mask otherwise, so
// formatting and encoding never reveal the value.
func (s Secret) String() string {
	if s.ref != "" {
		return s.ref
	}
	if s.value == "" {
		return ""
	}
	return Mask
}

// GoString masks the value under %#v.
func (s Secret) GoString() string { return s.String() }

// IsZero reports an empty secret, so omitempty and omitzero drop it.
func (s Secret) IsZero() bool { return s.ref == "" && s.value == "" }

// UnmarshalYAML reads the string as written; the loader resolves references
// and records both forms with NewSecretRef (ADR 0014).
func (s *Secret) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*s = Secret{value: raw}
	return nil
}

// MarshalJSON emits the reference or the mask, never the value.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON reads the string as written, the way UnmarshalYAML does, so an
// API accepts a reference, a literal, the mask it sent, or an empty string to
// remove the secret (ADR 0014).
func (s *Secret) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = Secret{value: raw}
	return nil
}

// Mask is what a secret reads as when its value is hidden. A client that
// receives the mask and sends it back unchanged is asking to keep the value it
// never saw, so a writer restores what the files hold.
const Mask = "***"

// MarshalYAML emits the reference or the mask, never the value.
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }

// JSONSchema describes Secret as a string.
func (Secret) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string"}
}
