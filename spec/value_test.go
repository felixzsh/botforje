package spec

import (
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestSecretRefKeepsAndShowsThePlaceholder(t *testing.T) {
	s := NewSecretRef("Bearer ${API_TOKEN}", "Bearer sk-live")
	if s.Reveal() != "Bearer sk-live" {
		t.Fatalf("Reveal = %q, want the resolved value", s.Reveal())
	}
	if s.Raw() != "Bearer ${API_TOKEN}" || !s.HasRef() {
		t.Fatalf("Raw = %q hasRef = %v", s.Raw(), s.HasRef())
	}
	if s.String() != "Bearer ${API_TOKEN}" || s.GoString() != s.String() {
		t.Fatalf("String = %q GoString = %q", s.String(), s.GoString())
	}
	if s.IsZero() {
		t.Fatal("a resolved secret is not zero")
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `"Bearer ${API_TOKEN}"` {
		t.Fatalf("JSON = %s, want the placeholder", encoded)
	}
}

func TestSecretMasksALiteral(t *testing.T) {
	s := NewSecret("super-secret")
	if s.Reveal() != "super-secret" {
		t.Fatal("Reveal must return the real value")
	}
	if s.String() != "***" || s.GoString() != "***" {
		t.Fatalf("secret leaks through formatting: %q %q", s.String(), s.GoString())
	}
	if s.HasRef() {
		t.Fatal("a literal must not report a reference")
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `"***"` {
		t.Fatalf("JSON = %s, want the mask", encoded)
	}
	if !(Secret{}).IsZero() {
		t.Fatal("the zero secret must be zero")
	}
}

func TestSecretUnmarshalYAMLReadsTheTextAsWritten(t *testing.T) {
	var s Secret
	if err := yaml.Unmarshal([]byte("${OPENAI_API_KEY}\n"), &s); err != nil {
		t.Fatal(err)
	}
	if s.Raw() != "${OPENAI_API_KEY}" {
		t.Fatalf("Raw = %q, want the text as written", s.Raw())
	}
	if s.HasRef() {
		t.Fatal("only the loader resolves a reference")
	}
	if s.Reveal() != "${OPENAI_API_KEY}" {
		t.Fatalf("Reveal = %q before interpolation", s.Reveal())
	}
}
