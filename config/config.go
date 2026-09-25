// Package config loads, interpolates, and validates Botflota configuration
// documents: files, includes, origin tracking, and semantic checks. The
// declarative objects themselves live in spec (ADR 0034).
package config

import (
	"os"
	"slices"

	"github.com/felixzsh/botflota/spec"
)

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
	RateLimit *RateLimitSettings `yaml:"rate_limit" json:"rate_limit,omitempty"`
	// ExclusiveScopes makes a scope grant exclusive: minting an access token
	// then refuses a scope another active token already holds, and startup
	// refuses to run while a scope is already granted to several active
	// tokens, so a scope belongs to at most one active token. The default
	// false lets any number of tokens share a scope; the owner token covers
	// every scope either way. Changing it requires a restart.
	ExclusiveScopes bool                  `yaml:"exclusive_scopes" json:"exclusive_scopes,omitempty"`
	Include         []string              `yaml:"include" json:"include,omitempty"`
	Scopes          map[string]spec.Scope `yaml:"scopes" json:"scopes"`

	origin     map[string]string `yaml:"-" json:"-"`
	source     string            `yaml:"-" json:"-"`
	files      []string          `yaml:"-"`
	watchPaths []string          `yaml:"-"`
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

// SourcePath is the absolute path of the main file this configuration was
// loaded from, which is where a writer must write back to.
func (c *Config) SourcePath() string { return c.source }

// WriteFile saves the whole document to path, with every secret as it is
// written, so a caller can lay down a starting configuration that loads the same
// way it will be read back (ADR 0002).
func (c *Config) WriteFile(path string) error {
	content, err := encode(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// Files lists the main config and every include Load read, so a watcher knows
// what to watch (ADR 0009).
func (c *Config) Files() []string { return slices.Clone(c.files) }

// WatchPaths includes loaded files and include directories, so a watcher also
// notices a new file matching a glob or directory include.
func (c *Config) WatchPaths() []string { return slices.Clone(c.watchPaths) }

// fragment is an included file: scopes only, no global settings (ADR 0016).
type fragment struct {
	Include []string              `yaml:"include" json:"include,omitempty"`
	Scopes  map[string]spec.Scope `yaml:"scopes" json:"scopes,omitempty"`
}
