package main

import (
	"fmt"
	"testing"

	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
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

// TestWhatsappSelectedFollowsTheSelector guards the lazy assembly: only a bot on
// the WhatsApp adapter makes the process open its device store, and a missing
// selector keeps the documented WhatsApp default (ADR 0054).
func TestWhatsappSelectedFollowsTheSelector(t *testing.T) {
	cfgWith := func(bots ...spec.Bot) *config.Config {
		scope := spec.Scope{Bots: map[string]spec.Bot{}}
		for i, bot := range bots {
			scope.Bots["bot"+fmt.Sprint(i)] = bot
		}
		return &config.Config{Scopes: map[string]spec.Scope{"dev": scope}}
	}
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"no bots", cfgWith(), false},
		{"another adapter only", cfgWith(spec.Bot{
			Graph:   "menu",
			Channel: spec.Channel{Provider: "fake", Mode: "text"},
		}), false},
		{"explicit whatsapp native", cfgWith(spec.Bot{
			Graph:   "menu",
			Channel: spec.Channel{Provider: "whatsapp", Mode: "native"},
		}), true},
		{"mixed fleet", cfgWith(
			spec.Bot{Graph: "menu", Channel: spec.Channel{Provider: "fake", Mode: "text"}},
			spec.Bot{Graph: "menu", Channel: spec.Channel{Provider: "whatsapp", Mode: "native"}},
		), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := whatsappSelected(tc.cfg); got != tc.want {
				t.Fatalf("whatsappSelected = %t, want %t", got, tc.want)
			}
		})
	}
}
