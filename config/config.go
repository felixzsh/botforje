// Package config loads, interpolates, and validates Botflota configuration
// documents: files, includes, origin tracking, and semantic checks. The
// declarative objects themselves live in spec (ADR 0034).
package config

import "github.com/felixzsh/botflota/spec"

// SchemaVersion is the only config version this build understands.
const SchemaVersion = 1

// Config is the root document: global settings plus scopes (ADR 0016).
type Config struct {
	Schema         string                `yaml:"$schema" json:"$schema,omitempty"`
	Version        int                   `yaml:"version" json:"version"` // must be 1
	Address        string                `yaml:"address" json:"address,omitempty"`
	Port           int                   `yaml:"port" json:"port,omitempty"`
	LogLevel       string                `yaml:"log_level" json:"log_level,omitempty"`
	LogLevelDeps   string                `yaml:"log_level_deps" json:"log_level_deps,omitempty"`
	DefaultTimeout int                   `yaml:"default_timeout" json:"default_timeout,omitempty"`
	DataDir        string                `yaml:"data_dir" json:"data_dir,omitempty"`
	Include        []string              `yaml:"include" json:"include,omitempty"`
	Scopes         map[string]spec.Scope `yaml:"scopes" json:"scopes"`

	origin map[string]string `yaml:"-" json:"-"`
	source string            `yaml:"-" json:"-"`
}

// fragment is an included file: scopes only, no global settings (ADR 0016).
type fragment struct {
	Include []string              `yaml:"include" json:"include,omitempty"`
	Scopes  map[string]spec.Scope `yaml:"scopes" json:"scopes,omitempty"`
}
