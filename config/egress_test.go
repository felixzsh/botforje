package config

import (
	"testing"

	"github.com/felixzsh/botflota/egress"
)

// TestEgressVocabularyMatchesTheRuntime keeps the setting's own validation and
// the policy the runtime builds on one vocabulary, so neither can drift alone.
func TestEgressVocabularyMatchesTheRuntime(t *testing.T) {
	for _, value := range []string{
		"", "open", "public_only", "private", "OPEN", "Open", " public_only",
	} {
		_, err := egress.ParseMode(value)
		if got, want := validEgress(value), err == nil; got != want {
			t.Fatalf("validEgress(%q) = %v, runtime accepts it = %v", value, got, want)
		}
	}
}
