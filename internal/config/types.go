// Package config loads, interpolates, and validates BotForje configuration.
// YAML lives here; the rest of the engine consumes these typed values.
package config

import (
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
	"go.yaml.in/yaml/v3"
)

// SchemaVersion is the only config version this build understands.
const SchemaVersion = 1

// Text is a message body: one string, or a list of variants picked at random.
type Text []string

// UnmarshalYAML accepts a scalar string or a sequence of strings.
func (t *Text) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		*t = Text{s}
	case yaml.SequenceNode:
		var ss []string
		if err := value.Decode(&ss); err != nil {
			return err
		}
		*t = Text(ss)
	default:
		return fmt.Errorf("body must be a string or a list of strings")
	}
	return nil
}

// MarshalYAML renders a single variant as a scalar and several as a list.
func (t Text) MarshalYAML() (any, error) {
	if len(t) == 1 {
		return t[0], nil
	}
	return []string(t), nil
}

// JSONSchema describes Text as a string or an array of strings.
func (Text) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		OneOf: []*jsonschema.Schema{
			{Type: "string"},
			{Type: "array", Items: &jsonschema.Schema{Type: "string"}},
		},
	}
}

// Secret is a value that must not leak into logs, errors, or the API
// (ADR 0014). Use Reveal to read it.
type Secret string

// Reveal returns the real value. Call it only where the secret is consumed.
func (s Secret) Reveal() string { return string(s) }

// String masks the value.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "***"
}

// GoString masks the value under %#v.
func (s Secret) GoString() string { return s.String() }

// MarshalJSON emits the masked value.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// MarshalYAML emits the masked value.
func (s Secret) MarshalYAML() (any, error) { return s.String(), nil }

// JSONSchema describes Secret as a string.
func (Secret) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string"}
}

// Config is the root document: global settings plus scopes (ADR 0016).
type Config struct {
	Schema         string           `yaml:"$schema" json:"$schema,omitempty"`
	Version        int              `yaml:"version" json:"version"` // must be 1
	Address        string           `yaml:"address" json:"address,omitempty"`
	Port           int              `yaml:"port" json:"port,omitempty"`
	LogLevel       string           `yaml:"log_level" json:"log_level,omitempty"`
	LogLevelDeps   string           `yaml:"log_level_deps" json:"log_level_deps,omitempty"`
	DefaultTimeout int              `yaml:"default_timeout" json:"default_timeout,omitempty"`
	DataDir        string           `yaml:"data_dir" json:"data_dir,omitempty"`
	Include        []string         `yaml:"include" json:"include,omitempty"`
	Scopes         map[string]Scope `yaml:"scopes" json:"scopes"`

	origin map[string]string `yaml:"-" json:"-"`
	source string            `yaml:"-" json:"-"`
}

// Scope is an isolation namespace holding its own entities (ADR 0016).
type Scope struct {
	LLMs    map[string]LLM    `yaml:"llms" json:"llms,omitempty"`
	Actions map[string]Action `yaml:"actions" json:"actions,omitempty"`
	Graphs  map[string]Graph  `yaml:"graphs" json:"graphs,omitempty"`
	Bots    map[string]Bot    `yaml:"bots" json:"bots,omitempty"`
}

// Fragment is an included file: scopes only, no global settings (ADR 0016).
type Fragment struct {
	Include []string         `yaml:"include" json:"include,omitempty"`
	Scopes  map[string]Scope `yaml:"scopes" json:"scopes,omitempty"`
}

// LLM is a named, scoped model profile referenced by a bot (ADR 0017).
type LLM struct {
	Provider    string  `yaml:"provider" json:"provider"`
	Model       string  `yaml:"model" json:"model"`
	APIKey      Secret  `yaml:"api_key" json:"api_key,omitempty"`
	BaseURL     string  `yaml:"base_url" json:"base_url,omitempty"`
	Routing     string  `yaml:"routing" json:"routing,omitempty"`
	Realization string  `yaml:"realization" json:"realization,omitempty"`
	Bounds      *Bounds `yaml:"bounds" json:"bounds,omitempty"`
}

// Bounds limits the tool loop inside a node (ADR 0008).
type Bounds struct {
	MaxToolsPerMessage int `yaml:"max_tools_per_message" json:"max_tools_per_message,omitempty"`
	Timeout            int `yaml:"timeout" json:"timeout,omitempty"`
}

// Action is a reusable pipeline of steps, optionally exposed as a tool.
type Action struct {
	Inputs map[string]Input `yaml:"inputs" json:"inputs,omitempty"`
	Guards *Guards          `yaml:"guards" json:"guards,omitempty"`
	Steps  []Step           `yaml:"steps" json:"steps"`
}

// Input is a named, typed argument of a tool (ADR 0008).
type Input struct {
	Type        string `yaml:"type" json:"type,omitempty"`
	Required    bool   `yaml:"required" json:"required,omitempty"`
	Default     any    `yaml:"default" json:"default,omitempty"`
	Description string `yaml:"description" json:"description,omitempty"`
	Bind        string `yaml:"bind" json:"bind,omitempty"`
}

// Guards limit how often an action runs.
type Guards struct {
	Cooldown *Cooldown `yaml:"cooldown" json:"cooldown,omitempty"`
}

// Cooldown blocks the same sender for Duration seconds and runs OnBlocked.
type Cooldown struct {
	Duration  int    `yaml:"duration" json:"duration"`
	OnBlocked []Step `yaml:"on_blocked" json:"on_blocked,omitempty"`
}

// Step is exactly one of message, request, or location.
type Step struct {
	Message  *Message  `yaml:"message" json:"message,omitempty"`
	Request  *Request  `yaml:"request" json:"request,omitempty"`
	Location *Location `yaml:"location" json:"location,omitempty"`
}

// Message is a reply: deterministic, LLM-realized, or anchored (ADR 0003).
type Message struct {
	Body   Text   `yaml:"body" json:"body,omitempty"`
	Intent string `yaml:"intent" json:"intent,omitempty"`
	Policy string `yaml:"policy" json:"policy,omitempty"`
}

// Request is an HTTP call whose result is addressable by Name.
type Request struct {
	Name    string            `yaml:"name" json:"name"`
	URL     string            `yaml:"url" json:"url"`
	Method  string            `yaml:"method" json:"method,omitempty"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
	Body    string            `yaml:"body" json:"body,omitempty"`
	Timeout int               `yaml:"timeout" json:"timeout,omitempty"`
	Retry   int               `yaml:"retry" json:"retry,omitempty"`
}

// Location is a map pin.
type Location struct {
	Latitude    float64 `yaml:"latitude" json:"latitude"`
	Longitude   float64 `yaml:"longitude" json:"longitude"`
	Name        string  `yaml:"name" json:"name,omitempty"`
	Address     string  `yaml:"address" json:"address,omitempty"`
	URL         string  `yaml:"url" json:"url,omitempty"`
	Description string  `yaml:"description" json:"description,omitempty"`
}

// Graph is a conversation state machine (ADR 0010, ADR 0015).
type Graph struct {
	Root           string          `yaml:"root" json:"root"`
	Timeout        int             `yaml:"timeout" json:"timeout,omitempty"`
	Fallback       string          `yaml:"fallback" json:"fallback,omitempty"`
	Routing        string          `yaml:"routing" json:"routing,omitempty"`
	InitialContext map[string]any  `yaml:"initial_context" json:"initial_context,omitempty"`
	Nodes          map[string]Node `yaml:"nodes" json:"nodes"`
}

// Node is a state (ADR 0003, ADR 0008).
type Node struct {
	Goal           string         `yaml:"goal" json:"goal,omitempty"`
	Policy         string         `yaml:"policy" json:"policy,omitempty"`
	OnEnter        []string       `yaml:"on_enter" json:"on_enter,omitempty"`
	OnExit         []string       `yaml:"on_exit" json:"on_exit,omitempty"`
	InitialContext map[string]any `yaml:"initial_context" json:"initial_context,omitempty"`
	Tools          []string       `yaml:"tools" json:"tools,omitempty"`
	Edges          []Edge         `yaml:"edges" json:"edges,omitempty"`
}

// Edge is a transition. An empty Intent marks the default (catch-all) edge.
type Edge struct {
	Intent   string   `yaml:"intent" json:"intent,omitempty"`
	Triggers []string `yaml:"triggers" json:"triggers,omitempty"`
	Goto     string   `yaml:"goto" json:"goto"`
}

// Bot binds one WhatsApp account to a graph, an optional llm, and settings.
type Bot struct {
	Graph    string       `yaml:"graph" json:"graph"`
	LLM      string       `yaml:"llm" json:"llm,omitempty"`
	Settings *BotSettings `yaml:"settings" json:"settings,omitempty"`
}

// BotSettings controls pacing and inbound filters (ADR 0011).
type BotSettings struct {
	QueueDelay      int      `yaml:"queue_delay" json:"queue_delay,omitempty"`
	QueueJitter     int      `yaml:"queue_jitter" json:"queue_jitter,omitempty"`
	SimulateTyping  *bool    `yaml:"simulate_typing" json:"simulate_typing,omitempty"`
	TypingDelay     int      `yaml:"typing_delay" json:"typing_delay,omitempty"`
	ReadReceipts    *bool    `yaml:"read_receipts" json:"read_receipts,omitempty"`
	MaxMsgPerMinute int      `yaml:"max_messages_per_minute" json:"max_messages_per_minute,omitempty"`
	IgnoreGroups    *bool    `yaml:"ignore_groups" json:"ignore_groups,omitempty"`
	AllowedSenders  []string `yaml:"allowed_senders" json:"allowed_senders,omitempty"`
	IgnoredSenders  []string `yaml:"ignored_senders" json:"ignored_senders,omitempty"`
}
