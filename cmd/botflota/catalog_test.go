package main

import (
	"testing"

	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// testCatalog is the installed set the CLI tests validate against, built the
// same way every config-only command builds it (ADR 0052, ADR 0054).
func testCatalog(t *testing.T) *channels.Catalog {
	t.Helper()
	catalog, err := pureCatalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return catalog
}

// TestSelectedKeysFollowsTheSelector guards the lazy assembly: only a bot whose
// selector the installed catalog resolves makes the process open that
// adapter's resources (ADR 0054).
func TestSelectedKeysFollowsTheSelector(t *testing.T) {
	installation := channels.Installation{Definition: channels.Definition{
		Provider: "fake", Modes: []string{"text", "rich"},
	}}
	catalog, err := channels.New(installation.Definition)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		name string
		bots []spec.Channel
		want bool
	}{
		{"no bots", nil, false},
		{"another provider", []spec.Channel{
			{Provider: "other", Mode: "text"},
		}, false},
		{"another mode of the same provider", []spec.Channel{
			{Provider: "fake", Mode: "local"},
		}, false},
		{"an installed mode", []spec.Channel{
			{Provider: "fake", Mode: "rich"},
		}, true},
		{"mixed fleet", []spec.Channel{
			{Provider: "other", Mode: "text"},
			{Provider: "fake", Mode: "text"},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selected := selectedKeys(catalog, configWithBots(t, tc.bots...))
			if got := selectedInstallation(installation, selected); got != tc.want {
				t.Fatalf("selected = %t, want %t", got, tc.want)
			}
		})
	}
}
