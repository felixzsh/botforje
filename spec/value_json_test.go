package spec

import (
	"encoding/json"
	"testing"
)

func TestTextUnmarshalJSON(t *testing.T) {
	cases := []struct {
		name string
		body string
		want Text
	}{
		{"one variant", `"hello"`, Text{"hello"}},
		{"several variants", `["hi","hey"]`, Text{"hi", "hey"}},
		{"an empty list", `[]`, Text{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var text Text
			if err := json.Unmarshal([]byte(tc.body), &text); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.body, err)
			}
			if len(text) != len(tc.want) {
				t.Fatalf("text = %v, want %v", text, tc.want)
			}
			for i := range text {
				if text[i] != tc.want[i] {
					t.Fatalf("text = %v, want %v", text, tc.want)
				}
			}
		})
	}

	t.Run("anything else is refused", func(t *testing.T) {
		var text Text
		if err := json.Unmarshal([]byte(`{"body":"hello"}`), &text); err == nil {
			t.Fatal("an object was accepted as a body")
		}
	})
}

func TestSecretUnmarshalJSON(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"a literal", `"sk-live"`, "sk-live"},
		{"a reference", `"${API_KEY}"`, "${API_KEY}"},
		{"the mask", `"***"`, "***"},
		{"an empty string", `""`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var secret Secret
			if err := json.Unmarshal([]byte(tc.body), &secret); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.body, err)
			}
			if secret.Raw() != tc.want {
				t.Fatalf("raw = %q, want %q", secret.Raw(), tc.want)
			}
		})
	}

	t.Run("a literal round-trips masked", func(t *testing.T) {
		var secret Secret
		if err := json.Unmarshal([]byte(`"sk-live"`), &secret); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		encoded, err := json.Marshal(secret)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(encoded) != `"***"` {
			t.Fatalf("encoded = %s, want the mask", encoded)
		}
		if secret.Reveal() != "sk-live" {
			t.Fatalf("reveal = %q, want the value", secret.Reveal())
		}
	})

	t.Run("an unresolved reference keeps its text", func(t *testing.T) {
		// Decoding takes the string as written, exactly as UnmarshalYAML does.
		// Only the loader turns it into a reference it has resolved, so a
		// document sent back before a load keeps the ${...} it was written
		// with.
		var secret Secret
		if err := json.Unmarshal([]byte(`"${API_KEY}"`), &secret); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if secret.Raw() != "${API_KEY}" {
			t.Fatalf("raw = %q, want the reference as written", secret.Raw())
		}
		if secret.HasRef() {
			t.Fatal("decoding resolved a reference; only the loader does that")
		}
	})

	t.Run("a non-string is refused", func(t *testing.T) {
		var secret Secret
		if err := json.Unmarshal([]byte(`123`), &secret); err == nil {
			t.Fatal("a number was accepted as a secret")
		}
	})
}
