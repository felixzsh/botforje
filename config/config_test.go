package config

import "testing"

func TestRateBudgets(t *testing.T) {
	off := 0
	custom := 42
	cases := []struct {
		name string
		cfg  *Config
		want RateBudgets
	}{
		{"defaults", &Config{}, RateBudgets{
			CredentialPerMinute: DefaultCredentialPerMinute,
			FailedPerMinute:     DefaultFailedPerMinute,
		}},
		{"override one field keeps the other", &Config{
			RateLimit: &RateLimitSettings{CredentialPerMinute: &custom},
		}, RateBudgets{
			CredentialPerMinute: 42,
			FailedPerMinute:     DefaultFailedPerMinute,
		}},
		{"explicit zero turns one budget off", &Config{
			RateLimit: &RateLimitSettings{CredentialPerMinute: &off},
		}, RateBudgets{
			CredentialPerMinute: 0,
			FailedPerMinute:     DefaultFailedPerMinute,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.RateBudgets(); got != tc.want {
				t.Fatalf("budgets = %+v, want %+v", got, tc.want)
			}
		})
	}
}
