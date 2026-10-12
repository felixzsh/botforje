package spec

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
	"go.yaml.in/yaml/v3"
)

// Bot binds one channel account to a graph, an optional llm, and settings.
type Bot struct {
	// DisplayName is an optional label for people-facing interfaces.
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	Graph       string `yaml:"graph" json:"graph"`
	LLM         string `yaml:"llm" json:"llm,omitempty"`
	// Channel selects the installed implementation this bot runs on; absent
	// means the documented WhatsApp native default.
	Channel  *Channel     `yaml:"channel" json:"channel,omitempty"`
	Settings *BotSettings `yaml:"settings" json:"settings,omitempty"`
}

// Channel selects one installed channel implementation: the messaging service,
// the account mode within it, and the adapter-owned options it declares.
type Channel struct {
	Provider string         `yaml:"provider" json:"provider"`
	Mode     string         `yaml:"mode" json:"mode"`
	Options  ChannelOptions `yaml:"options" json:"options,omitempty"`
}

// ChannelOptions holds the adapter-owned options of one bot's channel exactly
// as authored; the installed definition decodes and validates them strictly,
// and shared code never inspects them.
type ChannelOptions struct {
	node *yaml.Node
}

// UnmarshalYAML keeps the authored node for the definition to decode.
func (o *ChannelOptions) UnmarshalYAML(node *yaml.Node) error {
	o.node = node
	return nil
}

// MarshalYAML writes the options back exactly as they were read, so a
// publication preserves what its author declared.
func (o ChannelOptions) MarshalYAML() (any, error) {
	if o.node == nil {
		return nil, nil
	}
	return o.node, nil
}

// MarshalJSON renders the authored options, so an editor and a scope version
// see the same content the file holds.
func (o ChannelOptions) MarshalJSON() ([]byte, error) {
	if o.node == nil {
		return []byte("null"), nil
	}
	var value any
	if err := o.node.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// UnmarshalJSON accepts the same shape UnmarshalYAML takes.
func (o *ChannelOptions) UnmarshalJSON(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if len(node.Content) == 0 {
		o.node = nil
		return nil
	}
	o.node = node.Content[0]
	return nil
}

// IsZero reports whether no options were authored.
func (o ChannelOptions) IsZero() bool { return o.node == nil }

// Node returns the authored options node, or nil when the bot declared none.
func (o ChannelOptions) Node() *yaml.Node { return o.node }

// JSONSchema describes options as an open object; the catalog replaces it with
// the installed definition's own schema.
func (ChannelOptions) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object"}
}

// BotSettings controls the settings every channel shares. Everything a
// specific adapter owns lives in its channel options; a legacy key an adapter
// still reads from this block stays in Adapter until the transition ends.
type BotSettings struct {
	QueueDelay      int      `yaml:"queue_delay" json:"queue_delay,omitempty"`
	Jitter          int      `yaml:"jitter" json:"jitter,omitempty"`
	MaxMsgPerMinute int      `yaml:"max_messages_per_minute" json:"max_messages_per_minute,omitempty"`
	AllowedPeers    []string `yaml:"allowed_peers" json:"allowed_peers,omitempty"`
	IgnoredPeers    []string `yaml:"ignored_peers" json:"ignored_peers,omitempty"`
	// ExternalReplyPause is the milliseconds one chat stays handed to a human
	// after a message leaves the account without Botflota sending it. It is a
	// fleet policy, so it stays shared; observing the external reply at all is
	// the channel's declared capability and a channel without it refuses the
	// setting.
	ExternalReplyPause int `yaml:"external_reply_pause" json:"external_reply_pause,omitempty"`
	// Adapter captures settings keys the bot's channel definition owns, so an
	// existing WhatsApp document keeps loading. The definition validates them;
	// a definition that does not declare one rejects it.
	Adapter map[string]any `yaml:",inline" json:"-"`
}
