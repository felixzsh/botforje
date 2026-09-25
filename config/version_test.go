package config

import (
	"testing"

	"github.com/felixzsh/botflota/spec"
)

func TestScopeVersion(t *testing.T) {
	t.Run("same content yields the same version", func(t *testing.T) {
		first := spec.Scope{
			Bots: map[string]spec.Bot{"assistant": {Graph: "main"}},
			LLMs: map[string]spec.LLM{"primary": {Provider: "openai", Model: "gpt-4o"}},
		}
		second := spec.Scope{
			LLMs: map[string]spec.LLM{"primary": {Provider: "openai", Model: "gpt-4o"}},
			Bots: map[string]spec.Bot{"assistant": {Graph: "main"}},
		}
		one, err := ScopeVersion(first)
		if err != nil {
			t.Fatalf("ScopeVersion: %v", err)
		}
		two, err := ScopeVersion(second)
		if err != nil {
			t.Fatalf("ScopeVersion: %v", err)
		}
		if one != two {
			t.Fatalf("equal scopes versioned differently: %s and %s", one, two)
		}
	})

	t.Run("a changed field changes the version", func(t *testing.T) {
		before := spec.Scope{Bots: map[string]spec.Bot{"assistant": {Graph: "main"}}}
		after := spec.Scope{Bots: map[string]spec.Bot{"assistant": {Graph: "other"}}}
		if versionsEqual(t, before, after) {
			t.Fatal("a changed graph kept the same version")
		}
	})

	t.Run("a renamed entity is a different scope", func(t *testing.T) {
		before := spec.Scope{Bots: map[string]spec.Bot{"assistant": {}}}
		after := spec.Scope{Bots: map[string]spec.Bot{"support": {}}}
		if versionsEqual(t, before, after) {
			t.Fatal("a renamed bot kept the same version")
		}
	})

	t.Run("a display name is part of the version", func(t *testing.T) {
		before := spec.Scope{}
		after := spec.Scope{DisplayName: "Sales"}
		if versionsEqual(t, before, after) {
			t.Fatal("a display name edit kept the same version")
		}
	})

	t.Run("a changed secret changes the version", func(t *testing.T) {
		// A mask would hide the difference and let a stale write through.
		first := spec.Scope{LLMs: map[string]spec.LLM{
			"primary": {Provider: "openai", APIKey: spec.NewSecret("sk-one")},
		}}
		second := spec.Scope{LLMs: map[string]spec.LLM{
			"primary": {Provider: "openai", APIKey: spec.NewSecret("sk-two")},
		}}
		if versionsEqual(t, first, second) {
			t.Fatal("two literals produced one version")
		}
	})

	t.Run("a reference versions as written, not as resolved", func(t *testing.T) {
		// The document says ${API_KEY}; what the environment holds today is
		// not part of the saved scope, so only the reference is.
		ref := spec.Scope{LLMs: map[string]spec.LLM{
			"primary": {Provider: "openai", APIKey: spec.NewSecretRef("${API_KEY}", "sk-live")},
		}}
		same := spec.Scope{LLMs: map[string]spec.LLM{
			"primary": {Provider: "openai", APIKey: spec.NewSecretRef("${API_KEY}", "sk-other")},
		}}
		other := spec.Scope{LLMs: map[string]spec.LLM{
			"primary": {Provider: "openai", APIKey: spec.NewSecretRef("${OTHER_KEY}", "sk-live")},
		}}
		if !versionsEqual(t, ref, same) {
			t.Fatal("one reference produced two versions")
		}
		if versionsEqual(t, ref, other) {
			t.Fatal("two references produced one version")
		}
	})
}

func versionsEqual(t *testing.T, one, two spec.Scope) bool {
	t.Helper()
	left, err := ScopeVersion(one)
	if err != nil {
		t.Fatalf("ScopeVersion: %v", err)
	}
	right, err := ScopeVersion(two)
	if err != nil {
		t.Fatalf("ScopeVersion: %v", err)
	}
	return left == right
}
