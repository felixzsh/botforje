// Package channels holds the installed channel definitions and the immutable
// catalog the composition root injects into validation, schema generation, and
// fleet assembly (ADR 0052, ADR 0054). A definition is a pure declaration plus
// an optional builder; nothing here imports an adapter, performs channel I/O,
// or registers itself.
package channels

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/invopop/jsonschema"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// The documented missing-selector default: a bot without a channel block is
// WhatsApp native until every fixture and example makes the selector explicit
// (ADR 0054).
const (
	DefaultProvider = "whatsapp"
	DefaultMode     = "native"
)

// Capability feature names a definition advertises beyond content kinds. A name
// states what this adapter implements, never what a provider's platform allows
// (ADR 0052).
const (
	FeaturePresence          = "presence"
	FeatureHistoryImport     = "history_import"
	FeatureAddressBookImport = "address_book_import"
	FeatureExternalReply     = "external_reply_observation"
)

// Capabilities describes one installed implementation: the content kinds it can
// send and receive, the optional operations it implements, and its limits in
// provider-correct units (ADR 0052).
type Capabilities struct {
	Send     []botflota.MessageKind
	Receive  []botflota.MessageKind
	Features []string
	// Limits are provider-correct numeric bounds keyed by name.
	Limits map[string]int
}

// SupportsSend reports whether the definition can deliver kind.
func (c Capabilities) SupportsSend(kind botflota.MessageKind) bool {
	return containsKind(c.Send, kind)
}

// SupportsReceive reports whether the definition can ingest kind.
func (c Capabilities) SupportsReceive(kind botflota.MessageKind) bool {
	return containsKind(c.Receive, kind)
}

// HasFeature reports whether the definition implements one optional operation.
func (c Capabilities) HasFeature(feature string) bool {
	for _, have := range c.Features {
		if have == feature {
			return true
		}
	}
	return false
}

// Limit returns one declared numeric bound and whether the definition set it.
func (c Capabilities) Limit(name string) (int, bool) {
	value, ok := c.Limits[name]
	return value, ok
}

func containsKind(kinds []botflota.MessageKind, kind botflota.MessageKind) bool {
	for _, have := range kinds {
		if have == kind {
			return true
		}
	}
	return false
}

// Bind is one bot's request to build its channel: the neutral per-bot values
// plus the adapter-owned options its own Decode produced. Process-wide
// dependencies stay closed over in the adapter's builder (ADR 0052).
type Bind struct {
	Scope   string
	Bot     string
	Options any
	Log     *slog.Logger
}

// Definition is one installed channel implementation.
type Definition struct {
	// Provider is the messaging service; Kind names the adapter family; Modes
	// are the account modes this installation supports (ADR 0052).
	Provider string
	Kind     string
	Modes    []string
	// Capabilities states what this implementation can do.
	Capabilities Capabilities
	// OptionsSchema describes the adapter's options object for editors. It is
	// replaced into the generated schema under the definition's selector.
	OptionsSchema *jsonschema.Schema
	// Decode strictly decodes one bot's channel options and the adapter-owned
	// keys of its settings block into the adapter's typed value, or reports
	// why the author's values cannot be accepted. It is pure: no store opens,
	// no transport starts.
	Decode func(bot spec.Bot) (any, []string)
	// LegacySettings are the adapter-owned keys this definition still accepts
	// inside a bot's settings block, the compatibility half of the transition
	// to channel options (ADR 0054). Empty means the block holds only common
	// settings.
	LegacySettings []string
	// TemplateAliases maps a legacy reference this adapter owns to the neutral
	// path with the same meaning, applied per consuming bot and never to a
	// shared graph (ADR 0018/0020). An alias no definition declares fails
	// validation instead of rendering empty.
	TemplateAliases map[string]string
	// Build constructs the channel for one bot. A definition injected into a
	// config-only command leaves it nil; nothing invokes it there.
	Build func(Bind) (botflota.MessageChannel, error)
}

// Key identifies one installed implementation by provider and account mode.
type Key struct {
	Provider string
	Mode     string
}

// Catalog is the immutable set of installed definitions. It is built once by
// the composition root and read everywhere else.
type Catalog struct {
	defs  map[Key]Definition
	order []Key
}

// New builds a catalog from the installed definitions, rejecting a duplicate
// provider and mode and a definition that declares none.
func New(defs ...Definition) (*Catalog, error) {
	catalog := &Catalog{defs: make(map[Key]Definition, len(defs))}
	for _, def := range defs {
		if def.Provider == "" {
			return nil, errors.New("channels: definition without a provider")
		}
		if len(def.Modes) == 0 {
			return nil, fmt.Errorf(
				"channels: definition %q declares no account mode", def.Provider)
		}
		for _, mode := range def.Modes {
			key := Key{Provider: def.Provider, Mode: mode}
			if _, exists := catalog.defs[key]; exists {
				return nil, fmt.Errorf(
					"channels: provider %q mode %q is installed twice",
					key.Provider, key.Mode)
			}
			catalog.defs[key] = def
			catalog.order = append(catalog.order, key)
		}
	}
	sort.Slice(catalog.order, func(i, j int) bool {
		if catalog.order[i].Provider != catalog.order[j].Provider {
			return catalog.order[i].Provider < catalog.order[j].Provider
		}
		return catalog.order[i].Mode < catalog.order[j].Mode
	})
	return catalog, nil
}

// Lookup returns the definition installed for one provider and account mode.
func (c *Catalog) Lookup(provider, mode string) (Definition, bool) {
	if c == nil {
		return Definition{}, false
	}
	def, ok := c.defs[Key{Provider: provider, Mode: mode}]
	return def, ok
}

// Definitions lists the installed definitions in provider and mode order, one
// entry per provider and mode.
func (c *Catalog) Definitions() []Definition {
	if c == nil {
		return nil
	}
	defs := make([]Definition, 0, len(c.order))
	for _, key := range c.order {
		defs = append(defs, c.defs[key])
	}
	return defs
}

// Keys lists the installed provider and mode pairs in order.
func (c *Catalog) Keys() []Key {
	if c == nil {
		return nil
	}
	return append([]Key(nil), c.order...)
}

// Resolve returns the definition a bot's selector names. A missing selector
// means the documented WhatsApp native default; an explicit selection that is
// not installed is an error and never falls back (ADR 0054).
func (c *Catalog) Resolve(sel *spec.Channel) (Definition, Key, error) {
	key := Key{Provider: DefaultProvider, Mode: DefaultMode}
	if sel != nil {
		if sel.Provider == "" {
			return Definition{}, Key{}, errors.New("channel provider is required")
		}
		if sel.Mode == "" {
			return Definition{}, Key{}, errors.New("channel mode is required")
		}
		key = Key{Provider: sel.Provider, Mode: sel.Mode}
	}
	def, ok := c.Lookup(key.Provider, key.Mode)
	if !ok {
		if sel == nil {
			return Definition{}, Key{}, fmt.Errorf(
				"no channel is installed for the default %s/%s",
				key.Provider, key.Mode)
		}
		return Definition{}, Key{}, fmt.Errorf(
			"channel %s/%s is not installed", key.Provider, key.Mode)
	}
	return def, key, nil
}
