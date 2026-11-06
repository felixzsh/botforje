package config

import (
	"reflect"
	"slices"
	"testing"

	"github.com/felixzsh/botflota/spec"
)

func TestPlanReloadSeparatesGlobalAndScopeChanges(t *testing.T) {
	base := func() *Config {
		return &Config{
			Port: 3000,
			Scopes: map[string]spec.Scope{
				"support": {Bots: map[string]spec.Bot{
					"demo": {
						Graph:   "menu",
						Channel: spec.Channel{Provider: "whatsapp", Mode: "native"},
						Settings: &spec.BotSettings{
							QueueDelay: 900,
						},
					},
				}},
			},
		}
	}
	botChannel := func(c *Config, change func(*spec.Channel)) {
		scope := c.Scopes["support"]
		bot := scope.Bots["demo"]
		channel := bot.Channel
		change(&channel)
		bot.Channel = channel
		scope.Bots["demo"] = bot
	}
	botSettings := func(c *Config, change func(*spec.BotSettings)) {
		scope := c.Scopes["support"]
		bot := scope.Bots["demo"]
		settings := *bot.Settings
		change(&settings)
		bot.Settings = &settings
		scope.Bots["demo"] = bot
	}

	cases := []struct {
		name   string
		change func(*Config)
		want   []string
	}{
		{"unchanged", func(*Config) {}, nil},
		{"global", func(c *Config) { c.Port = 4000 },
			[]string{"global setting port"}},
		{"actions adopt", func(c *Config) {
			scope := c.Scopes["support"]
			scope.Actions = map[string]spec.Action{
				"greet": {Steps: []spec.Step{
					{Message: &spec.Message{Body: spec.Text{"hi"}}},
				}},
			}
			c.Scopes["support"] = scope
		}, nil},
		{"filter adopts", func(c *Config) {
			botSettings(c, func(s *spec.BotSettings) {
				s.IgnoredPeers = []string{"999"}
			})
		}, nil},
		{"channel settings", func(c *Config) {
			botSettings(c, func(s *spec.BotSettings) { s.QueueDelay = 100 })
		}, []string{
			`channel settings of bot "demo" in scope "support"`,
		}},
		{"channel mode change", func(c *Config) {
			botChannel(c, func(ch *spec.Channel) { ch.Mode = "cloud" })
		}, []string{
			`channel settings of bot "demo" in scope "support"`,
		}},
		{"channel options change", func(c *Config) {
			botChannel(c, func(ch *spec.Channel) {
				ch.Options = channelOptions(t, "read_receipts: true")
			})
		}, []string{
			`channel settings of bot "demo" in scope "support"`,
		}},
		{"bot added", func(c *Config) {
			c.Scopes["support"].Bots["other"] = spec.Bot{Graph: "menu"}
		}, []string{`bot "other" of scope "support" added`}},
		{"scope removed", func(c *Config) { delete(c.Scopes, "support") },
			[]string{`scope "support" removed`}},
		{"rate limit", func(c *Config) {
			off := 0
			c.RateLimit = &RateLimitSettings{CredentialPerMinute: &off}
		}, []string{"global setting rate_limit"}},
		{"exclusive scopes", func(c *Config) {
			c.ExclusiveScopes = true
		}, []string{"global setting exclusive_scopes"}},
		{"egress", func(c *Config) { c.Egress = "public_only" },
			[]string{"global setting egress"}},
		{"webhooks", func(c *Config) {
			c.Webhooks = []WebhookSettings{{
				URL:    "https://hooks.example.test/events",
				Secret: spec.NewSecret("secret"),
				Events: []string{"contact.first_seen"},
			}}
		}, []string{"global setting webhooks"}},
		{"file roots", func(c *Config) {
			c.FileRoots = map[string]string{"support": "/srv/scopes/support"}
		}, []string{"global setting file_roots"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current, next := base(), base()
			tc.change(next)
			plan := PlanReload(current, next)
			reasons := append([]string(nil), plan.Global...)
			for _, scope := range plan.Scopes {
				reasons = append(reasons, plan.ScopeReasons[scope]...)
			}
			if !slices.Equal(reasons, tc.want) {
				t.Fatalf("reasons = %v, want %v", reasons, tc.want)
			}
		})
	}
}

func TestPlanReloadChangesOnlyTheEditedScope(t *testing.T) {
	current := &Config{Egress: "public_only", Scopes: map[string]spec.Scope{
		"a": {Actions: map[string]spec.Action{"greet": {
			Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"hi"}}}},
		}}},
		"b": {Actions: map[string]spec.Action{"greet": {
			Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"hello"}}}},
		}}},
	}}
	next := &Config{Egress: "open", Scopes: map[string]spec.Scope{
		"a": {Actions: map[string]spec.Action{"greet": {
			Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"changed"}}}},
		}}},
		"b": current.Scopes["b"],
	}}
	plan := PlanReload(current, next)
	if !slices.Equal(plan.Scopes, []string{"a"}) {
		t.Fatalf("changed scopes = %v, want [a]", plan.Scopes)
	}
	if !slices.Equal(plan.Global, []string{"global setting egress"}) {
		t.Fatalf("global reasons = %v, want only egress", plan.Global)
	}
	// A publication activates scopes only: the instance policy it never owns
	// stays the running one, so changing it is restart-required (ADR 0048).
	if active := WithScopes(current, next, plan.Scopes); active.Egress != "public_only" {
		t.Fatalf("active egress = %q, want the running %q", active.Egress, "public_only")
	}
	active := WithScopes(current, next, plan.Scopes)
	if !reflect.DeepEqual(active.Scopes["b"], current.Scopes["b"]) {
		t.Fatal("unchanged scope b was changed in the active snapshot")
	}
}

// TestWithScopesKeepsTheActiveFileRoots: adopting a scope never adopts the
// candidate's roots, because a file root is owner-managed and restart-required
// (ADR 0047).
func TestWithScopesKeepsTheActiveFileRoots(t *testing.T) {
	current := &Config{
		FileRoots: map[string]string{"a": "/srv/scopes/a"},
		Scopes:    map[string]spec.Scope{"a": {}},
	}
	next := &Config{
		FileRoots: map[string]string{"a": "/srv/scopes/a", "b": "/srv/scopes/b"},
		Scopes:    map[string]spec.Scope{"a": {}, "b": {}},
	}
	plan := PlanReload(current, next)
	active := WithScopes(current, next, plan.Scopes)
	if !reflect.DeepEqual(active.FileRoots, current.FileRoots) {
		t.Fatalf("active file roots = %v, want the running %v",
			active.FileRoots, current.FileRoots)
	}
	if !slices.Contains(plan.Global, "global setting file_roots") {
		t.Fatalf("global changes = %v, want file_roots restart-required", plan.Global)
	}
}
