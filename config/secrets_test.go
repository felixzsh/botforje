package config

import (
	"testing"

	"github.com/felixzsh/botflota/spec"
)

func TestRestoreMaskedSecrets(t *testing.T) {
	saved := spec.Scope{
		LLMs: map[string]spec.LLM{"primary": {
			Provider: "openai", Model: "gpt-4o",
			APIKey: spec.NewSecret("sk-saved"),
		}},
		Actions: map[string]spec.Action{"call": {Steps: []spec.Step{{
			Request: &spec.Request{Name: "call", Headers: map[string]spec.Secret{
				"Authorization": spec.NewSecret("Bearer sk-saved"),
			}},
		}}}},
	}

	t.Run("a mask keeps the saved credential", func(t *testing.T) {
		submitted := spec.Scope{
			LLMs: map[string]spec.LLM{"primary": {
				Provider: "openai", Model: "gpt-4o", APIKey: spec.NewSecret("***"),
			}},
			Actions: map[string]spec.Action{"call": {Steps: []spec.Step{{
				Request: &spec.Request{Name: "call", Headers: map[string]spec.Secret{
					"Authorization": spec.NewSecret("***"),
				}},
			}}}},
		}
		restored := RestoreMaskedSecrets(saved, submitted)
		if got := restored.LLMs["primary"].APIKey.Reveal(); got != "sk-saved" {
			t.Fatalf("api_key = %q, want the saved credential", got)
		}
		header := restored.Actions["call"].Steps[0].Request.Headers["Authorization"]
		if header.Reveal() != "Bearer sk-saved" {
			t.Fatalf("header = %q, want the saved credential", header.Reveal())
		}
	})

	t.Run("a new value replaces the credential", func(t *testing.T) {
		submitted := spec.Scope{LLMs: map[string]spec.LLM{"primary": {
			APIKey: spec.NewSecret("sk-new"),
		}}}
		restored := RestoreMaskedSecrets(saved, submitted)
		if got := restored.LLMs["primary"].APIKey.Reveal(); got != "sk-new" {
			t.Fatalf("api_key = %q, want the new credential", got)
		}
	})

	t.Run("a reference replaces the credential and keeps its form", func(t *testing.T) {
		submitted := spec.Scope{LLMs: map[string]spec.LLM{"primary": {
			APIKey: spec.NewSecret("${API_KEY}"),
		}}}
		restored := RestoreMaskedSecrets(saved, submitted)
		if got := restored.LLMs["primary"].APIKey.Raw(); got != "${API_KEY}" {
			t.Fatalf("api_key = %q, want the reference", got)
		}
	})

	t.Run("an empty value removes the credential", func(t *testing.T) {
		submitted := spec.Scope{LLMs: map[string]spec.LLM{"primary": {
			APIKey: spec.NewSecret(""),
		}}}
		restored := RestoreMaskedSecrets(saved, submitted)
		if got := restored.LLMs["primary"].APIKey.Raw(); got != "" {
			t.Fatalf("api_key = %q, want it removed", got)
		}
	})

	t.Run("a saved reference comes back as a reference", func(t *testing.T) {
		withRef := spec.Scope{LLMs: map[string]spec.LLM{"primary": {
			APIKey: spec.NewSecretRef("${API_KEY}", "sk-resolved"),
		}}}
		submitted := spec.Scope{LLMs: map[string]spec.LLM{"primary": {
			APIKey: spec.NewSecret("***"),
		}}}
		restored := RestoreMaskedSecrets(withRef, submitted)
		key := restored.LLMs["primary"].APIKey
		if !key.HasRef() || key.Raw() != "${API_KEY}" {
			t.Fatalf("api_key = %q, want the reference as written", key.Raw())
		}
		if key.Reveal() != "sk-resolved" {
			t.Fatalf("api_key resolved to %q, want the saved value", key.Reveal())
		}
	})

	t.Run("a scope the client did not send is not resurrected", func(t *testing.T) {
		restored := RestoreMaskedSecrets(saved, spec.Scope{})
		if len(restored.LLMs) != 0 || len(restored.Actions) != 0 {
			t.Fatal("omitted entities came back")
		}
	})

	t.Run("a secret with nothing saved stays as sent", func(t *testing.T) {
		submitted := spec.Scope{Actions: map[string]spec.Action{"new": {Steps: []spec.Step{{
			Request: &spec.Request{Name: "new", Headers: map[string]spec.Secret{
				"X-Key": spec.NewSecret("fresh"),
			}},
		}}}}}
		restored := RestoreMaskedSecrets(saved, submitted)
		got := restored.Actions["new"].Steps[0].Request.Headers["X-Key"]
		if got.Raw() != "fresh" {
			t.Fatalf("header = %q, want the submitted value", got.Raw())
		}
	})
}
