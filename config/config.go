// Package config loads, interpolates, and validates Botflota configuration
// documents: files, includes, origin tracking, and semantic checks. The
// declarative objects themselves live in spec (ADR 0034).
package config

import "github.com/felixzsh/botflota/spec"

// SchemaVersion is the only config version this build understands.
const SchemaVersion = 1

// Built-in per-minute budgets of the API, used when rate_limit is absent.
const (
	DefaultCredentialPerMinute = 600
	DefaultFailedPerMinute     = 60
)

// RateLimitSettings are the API's per-minute request budgets. An omitted
// block uses the defaults, an omitted field keeps its default, and an
// explicit zero turns that budget off.
type RateLimitSettings struct {
	CredentialPerMinute *int `yaml:"credential_per_minute" json:"credential_per_minute,omitempty"`
	FailedPerMinute     *int `yaml:"failed_per_minute" json:"failed_per_minute,omitempty"`
}

// RateBudgets are the resolved budgets the API enforces.
type RateBudgets struct {
	CredentialPerMinute int
	FailedPerMinute     int
}

// Config is the root document: instance settings plus the scopes it runs.
type Config struct {
	Schema         string `yaml:"$schema" json:"$schema,omitempty"`
	Version        int    `yaml:"version" json:"version"` // must be 1
	Address        string `yaml:"address" json:"address,omitempty"`
	Port           int    `yaml:"port" json:"port,omitempty"`
	LogLevel       string `yaml:"log_level" json:"log_level,omitempty"`
	LogLevelDeps   string `yaml:"log_level_deps" json:"log_level_deps,omitempty"`
	DefaultTimeout int    `yaml:"default_timeout" json:"default_timeout,omitempty"`
	DataDir        string `yaml:"data_dir" json:"data_dir,omitempty"`
	// DeviceName is the companion name WhatsApp shows in Linked devices. It is
	// sent only when a bot registers a new pairing; an existing link keeps the
	// name it was registered with. Empty uses the built-in default.
	DeviceName string `yaml:"device_name" json:"device_name,omitempty"`
	// RateLimit bounds the API per fixed minute; an omitted block uses the
	// built-in budgets, and changing it requires a restart.
	RateLimit *RateLimitSettings    `yaml:"rate_limit" json:"rate_limit,omitempty"`
	Include   []string              `yaml:"include" json:"include,omitempty"`
	Scopes    map[string]spec.Scope `yaml:"scopes" json:"scopes"`

	origin map[string]string `yaml:"-" json:"-"`
	source string            `yaml:"-" json:"-"`
	files  []string          `yaml:"-"`
}

// RateBudgets resolves the per-minute budgets: the defaults unless
// rate_limit overrides a field, where an explicit zero turns it off.
func (c *Config) RateBudgets() RateBudgets {
	budgets := RateBudgets{
		CredentialPerMinute: DefaultCredentialPerMinute,
		FailedPerMinute:     DefaultFailedPerMinute,
	}
	if c.RateLimit == nil {
		return budgets
	}
	if c.RateLimit.CredentialPerMinute != nil {
		budgets.CredentialPerMinute = *c.RateLimit.CredentialPerMinute
	}
	if c.RateLimit.FailedPerMinute != nil {
		budgets.FailedPerMinute = *c.RateLimit.FailedPerMinute
	}
	return budgets
}

// Files lists the main config and every include Load read, so a watcher knows
// what to watch (ADR 0009).
func (c *Config) Files() []string { return c.files }

// fragment is an included file: scopes only, no global settings (ADR 0016).
type fragment struct {
	Include []string              `yaml:"include" json:"include,omitempty"`
	Scopes  map[string]spec.Scope `yaml:"scopes" json:"scopes,omitempty"`
}
