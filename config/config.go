// Package config loads, interpolates, and validates Botflota configuration
// documents: files, includes, origin tracking, and semantic checks. The
// declarative objects themselves live in spec (ADR 0034).
package config

import (
	"os"
	"slices"

	"github.com/felixzsh/botflota/egress"
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

// WebhookSettings is one instance-level webhook target: where deliveries go,
// the secret their bodies are signed with, and the events it subscribes to.
// Only the main config file declares it, and changing it requires a restart
// like every other instance setting.
type WebhookSettings struct {
	URL    string      `yaml:"url" json:"url"`
	Secret spec.Secret `yaml:"secret" json:"secret"`
	Events []string    `yaml:"events" json:"events"`
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
	// RateLimit bounds the API per fixed minute; an omitted block uses the
	// built-in budgets, and changing it requires a restart.
	RateLimit *RateLimitSettings `yaml:"rate_limit" json:"rate_limit,omitempty"`
	// ExclusiveScopes makes a scope grant exclusive: minting an access token
	// then refuses a scope another active token already holds, and startup
	// refuses to run while a scope is already granted to several active
	// tokens, so a scope belongs to at most one active token. The default
	// false lets any number of tokens share a scope; the owner token covers
	// every scope either way. Changing it requires a restart.
	ExclusiveScopes bool `yaml:"exclusive_scopes" json:"exclusive_scopes,omitempty"`
	// FileRoots bounds the local files one scope's steps may read, keyed by
	// scope: a media step's local path is then relative to that root and may
	// not leave it. A scope without an entry reads any file the process can.
	// Only the main config file declares it, and only the owner sets it; each
	// root must be an absolute existing directory that no other root contains
	// or is contained by. Changing it requires a restart. It bounds local
	// reads only, not network destinations, secrets, or resource use.
	FileRoots map[string]string `yaml:"file_roots" json:"file_roots,omitempty"`
	// SecretFiles names the owner-managed secret catalog of a scope, by scope
	// key: a ${NAME} in one of that scope's definitions then resolves only
	// from this file, with no fallback to the process environment, and a name
	// the file does not hold is an error. The file holds NAME=value lines and
	// `#` comments; it is read as data, never executed, a value is kept verbatim
	// after the first `=`, and a line with no `=`, an empty value, or a repeated
	// name fails the load naming the line and the name, never a value. It must be
	// a regular file of at most 1 MiB, which load refuses before opening it and
	// reads at most one byte past. Only the main config file declares it and only
	// the owner sets it, so a published scope can never set or change it. A
	// relative path is relative to the main config file, and the file must sit
	// outside every file root so it can never be sent as media. A scope without
	// an entry keeps resolving from the process environment. Changing it requires
	// a restart.
	SecretFiles map[string]string `yaml:"secret_files" json:"secret_files,omitempty"`
	// Egress bounds where an outbound HTTP call may go: "open" (the default)
	// reaches any destination, "public_only" only a public one. It is set by
	// the instance owner, and changing it requires a restart; a published
	// scope cannot set it.
	Egress string `yaml:"egress" json:"egress,omitempty"`
	// Webhooks are the outbound event deliveries this instance sends; see
	// WebhookSettings.
	Webhooks []WebhookSettings     `yaml:"webhooks" json:"webhooks,omitempty"`
	Include  []string              `yaml:"include" json:"include,omitempty"`
	Scopes   map[string]spec.Scope `yaml:"scopes" json:"scopes"`

	origin     map[entityRef]string `yaml:"-" json:"-"`
	source     string               `yaml:"-" json:"-"`
	files      []string             `yaml:"-"`
	watchPaths []string             `yaml:"-"`
	// catalogs holds each scope's secret names as its file holds them, so
	// rotating a catalog is a restart-only change the reload plan can see
	// (ADR 0049).
	catalogs map[string]secretCatalog `yaml:"-" json:"-"`
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

// EgressMode resolves the outbound HTTP policy. Validate rejects an unknown
// value, so one that reaches here unvalidated falls back to the open policy.
func (c *Config) EgressMode() egress.Mode {
	mode, err := egress.ParseMode(c.Egress)
	if err != nil {
		return egress.Open
	}
	return mode
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
