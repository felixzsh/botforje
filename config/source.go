package config

import "sync/atomic"

// Source is the active configuration shared by the fleet and API. A reload
// replaces only the scopes it activates; saved but pending YAML stays outside
// this snapshot (ADR 0009).
type Source struct {
	current atomic.Pointer[Config]
}

func NewSource(cfg *Config) *Source {
	source := &Source{}
	source.Replace(cfg)
	return source
}

func (s *Source) Current() *Config { return s.current.Load() }

func (s *Source) Replace(cfg *Config) { s.current.Store(cfg) }
