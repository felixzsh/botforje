package config

import (
	"slices"
	"testing"

	"github.com/felixzsh/botflota/egress"
)

// TestEgressVocabularyMatchesTheRuntime keeps the setting's own validation and
// the policy the runtime builds on one vocabulary: both sets are compared, so
// neither can gain or lose a value alone.
func TestEgressVocabularyMatchesTheRuntime(t *testing.T) {
	configSet := egressModes()
	runtimeSet := make([]string, 0, len(egress.Modes()))
	for _, mode := range egress.Modes() {
		runtimeSet = append(runtimeSet, string(mode))
	}
	slices.Sort(configSet)
	slices.Sort(runtimeSet)
	if !slices.Equal(configSet, runtimeSet) {
		t.Fatalf("config accepts %v, the runtime accepts %v", configSet, runtimeSet)
	}
	defaultMode, err := egress.ParseMode("")
	if err != nil {
		t.Fatalf("ParseMode(\"\"): %v", err)
	}
	if defaultMode != egress.Open || !validEgress("") {
		t.Fatalf("the empty default is not shared: %q", defaultMode)
	}
	for _, value := range []string{"private", "OPEN", "public-only"} {
		if validEgress(value) {
			t.Errorf("config accepts %q, which names no policy", value)
		}
		if _, err := egress.ParseMode(value); err == nil {
			t.Errorf("the runtime accepts %q, which names no policy", value)
		}
	}
}
