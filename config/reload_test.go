package config

import (
	"slices"
	"testing"

	"github.com/felixzsh/botflota/spec"
)

func TestRequiresRestartClassifiesChanges(t *testing.T) {
	enabled := true
	base := func() *Config {
		return &Config{
			Port: 3000,
			Scopes: map[string]spec.Scope{
				"support": {Bots: map[string]spec.Bot{
					"demo": {Graph: "menu", Settings: &spec.BotSettings{
						ReplyDelay:   900,
						IgnoreGroups: &enabled,
					}},
				}},
			},
		}
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
				s.IgnoredSenders = []string{"999"}
			})
		}, nil},
		{"channel settings", func(c *Config) {
			botSettings(c, func(s *spec.BotSettings) { s.ReplyDelay = 100 })
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current, next := base(), base()
			tc.change(next)
			if got := RequiresRestart(current, next); !slices.Equal(got, tc.want) {
				t.Fatalf("reasons = %v, want %v", got, tc.want)
			}
		})
	}
}
