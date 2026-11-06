// Package channels holds the installed channel definitions and the immutable
// catalog the composition root injects into validation, schema generation, and
// fleet assembly (ADR 0052, ADR 0054). A definition is a pure declaration plus
// an optional builder; nothing here imports an adapter, performs channel I/O,
// or registers itself.
package channels

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"

	"github.com/invopop/jsonschema"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
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
	// CardVariants are the contact-card entry kinds this definition can
	// compose onto its own native card object (ADR 0036).
	CardVariants []spec.ContactEntryKind
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

// SupportsCardVariant reports whether the definition can compose one contact
// card entry kind (ADR 0036).
func (c Capabilities) SupportsCardVariant(kind spec.ContactEntryKind) bool {
	return slices.Contains(c.CardVariants, kind)
}

// clone copies the slices so the catalog's copy cannot be changed through one a
// caller received (ADR 0052).
func (c Capabilities) clone() Capabilities {
	c.Send = slices.Clone(c.Send)
	c.Receive = slices.Clone(c.Receive)
	c.Features = slices.Clone(c.Features)
	c.CardVariants = slices.Clone(c.CardVariants)
	return c
}

func containsKind(kinds []botflota.MessageKind, kind botflota.MessageKind) bool {
	for _, have := range kinds {
		if have == kind {
			return true
		}
	}
	return false
}

// ContentValidator reports what one action cannot send on this implementation,
// as complete reasons the author can act on. It is pure: no store opens and no
// transport starts. A definition uses it for the limits and variants that its
// capabilities alone do not express (ADR 0052).
type ContentValidator func(key Key, actionName string, action spec.Action) []string

// ValidateDeclaredContent is the default ContentValidator: it checks every
// step's content kind and contact-card variant against caps. An adapter supplies
// its own ContentValidator to add provider-correct limits on top; the two run
// together, so a hook never replaces this base check.
func ValidateDeclaredContent(caps Capabilities) ContentValidator {
	caps = caps.clone()
	return func(key Key, actionName string, action spec.Action) []string {
		var reasons []string
		check := func(steps []spec.Step, where string) {
			prefix := ""
			if where != "" {
				prefix = where + " "
			}
			for i, step := range steps {
				report := func(what string) {
					reasons = append(reasons, fmt.Sprintf(
						"action %q %sstep %d: channel %s/%s cannot %s",
						actionName, prefix, i, key.Provider, key.Mode, what))
				}
				if step.Contact != nil {
					for _, entry := range step.Contact.Entries {
						variant := spec.ContactEntryKind(entry.Kind)
						if variant.Valid() && !caps.SupportsCardVariant(variant) {
							report(fmt.Sprintf("compose contact entry kind %q", entry.Kind))
						}
					}
				}
				kind, ok := stepKind(step)
				if !ok {
					continue
				}
				if !caps.SupportsSend(kind) {
					report("send " + string(kind))
				}
			}
		}
		check(action.Steps, "")
		if action.Guards != nil {
			if action.Guards.Cooldown != nil {
				check(action.Guards.Cooldown.OnBlocked, "cooldown.on_blocked")
			}
			if action.Guards.RateLimit != nil {
				check(action.Guards.RateLimit.OnBlocked, "rate_limit.on_blocked")
			}
		}
		return reasons
	}
}

// stepKind is the content kind one step sends, or false for a step that emits
// no channel content or whose kind is checked elsewhere.
func stepKind(step spec.Step) (botflota.MessageKind, bool) {
	switch {
	case step.Message != nil:
		return botflota.KindText, true
	case step.Media != nil:
		kind := botflota.MessageKind(step.Media.Kind)
		if !botflota.MediaType(kind).Valid() {
			return "", false
		}
		return kind, true
	case step.Location != nil:
		return botflota.KindLocation, true
	case step.Contact != nil:
		return botflota.KindContact, true
	case step.Poll != nil:
		return botflota.KindPoll, true
	}
	return "", false
}

// Bind is one bot's request to build its channel: the neutral per-bot values
// plus the adapter-owned options its own Decode produced. Process-wide
// dependencies stay closed over in the adapter's builder (ADR 0052).
type Bind struct {
	Scope   string
	Bot     string
	Options any
	// FileRoot is the scope's owner-set root for local file reads, empty when
	// the owner set none. A channel that opens a local path passes it to
	// botflota.OpenLocalFile instead of opening the path itself (ADR 0047).
	FileRoot string
	Log      *slog.Logger
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
	// OptionsSchema returns a fresh editor schema of the adapter's options
	// object, or nil when it declares none. It is a function so the catalog
	// never shares a mutable schema value (ADR 0052).
	OptionsSchema func() *jsonschema.Schema
	// Decode strictly decodes one bot's channel options into the adapter's
	// typed value, or reports why the author's values cannot be accepted. It is
	// pure: no store opens, no transport starts.
	Decode func(bot spec.Bot) (any, []string)
	// ValidateContent adds what one action cannot send on this implementation
	// beyond the declared capabilities, such as a provider-correct limit. It is
	// additional: shared validation always checks content kinds and card variants
	// against Capabilities first, so a hook never disables that base check. A nil
	// hook adds nothing (ADR 0052).
	ValidateContent ContentValidator
	// ValidateTarget reports why an explicit conversation target cannot be
	// addressed on this implementation, as reasons the caller can act on.
	// It is pure: no store opens and no transport starts. A nil hook allows
	// every well-formed target; API admission always checks the common shape
	// and the provider match first, so a hook never replaces those base
	// checks (ADR 0044, ADR 0052).
	ValidateTarget func(target botflota.ChatTarget) []string
	// CompatibleBindings reports why one bot's history cannot move to
	// another bot's binding, as reasons the caller can act on. It is pure
	// and adapter-owned: shared code never parses opaque subjects or
	// assumes a provider match alone proves compatibility. A nil hook
	// means unknown compatibility, which history migration refuses
	// (ADR 0042, ADR 0051).
	CompatibleBindings func(from, to botflota.Binding) []string
	// MessageSpace reports the adapter-declared message-ID space of one
	// committed binding: the provider, account, or profile context a
	// provider id needs for uniqueness (ADR 0026, ADR 0051). The space
	// survives reconnect, restart, and reauthorization of the same
	// account; a different account is a different space, and a binding
	// generation change does not by itself change it. It is pure and
	// adapter-owned: shared code threads the opaque value without
	// parsing it. A nil hook means the provider's ids need no space.
	MessageSpace func(binding *botflota.Binding) string
	// Build constructs the channel for one bot. A definition injected into a
	// config-only command leaves it nil; nothing invokes it there.
	Build func(Bind) (botflota.MessageChannel, error)
	// Prepare is an optional hook run before one bot joins the fleet: a bot
	// whose local credentials are missing is skipped without stopping its
	// peers, and err is fatal to startup (ADR 0022, ADR 0053). A nil Prepare
	// means always ready.
	Prepare func(ctx context.Context, bind Bind) (ready bool, state string, err error)
	// SetupMethods names the account-setup methods this implementation
	// supports for one bot ("qr", a credential handoff, a multi-step
	// approval, a managed-profile selection). Empty means the
	// implementation offers no remote setup (ADR 0053).
	SetupMethods []string
	// Setup returns one bot's neutral account lifecycle and setup runner.
	// A definition injected into a config-only command leaves it nil;
	// nothing invokes it there. Either return may be nil when the
	// implementation does not offer that half.
	Setup func(Bind) (botflota.AccountLifecycle, botflota.SetupRunner, error)
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
			catalog.defs[key] = def.clone()
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
	return def.clone(), ok
}

// Definitions lists the installed definitions in provider and mode order, one
// entry per provider and mode.
func (c *Catalog) Definitions() []Definition {
	if c == nil {
		return nil
	}
	defs := make([]Definition, 0, len(c.order))
	for _, key := range c.order {
		defs = append(defs, c.defs[key].clone())
	}
	return defs
}

// clone copies the mutable parts of a definition, so a caller can never change
// what the catalog holds through a slice or map it received (ADR 0052).
func (d Definition) clone() Definition {
	d.Modes = slices.Clone(d.Modes)
	d.Capabilities = d.Capabilities.clone()
	d.SetupMethods = slices.Clone(d.SetupMethods)
	return d
}

// Keys lists the installed provider and mode pairs in order.
func (c *Catalog) Keys() []Key {
	if c == nil {
		return nil
	}
	return append([]Key(nil), c.order...)
}

// Resolve returns the definition a bot's selector names. A selection that is
// incomplete or not installed is an error and never falls back (ADR 0054).
func (c *Catalog) Resolve(sel spec.Channel) (Definition, Key, error) {
	if sel.Provider == "" {
		return Definition{}, Key{}, errors.New("channel provider is required")
	}
	if sel.Mode == "" {
		return Definition{}, Key{}, errors.New("channel mode is required")
	}
	key := Key{Provider: sel.Provider, Mode: sel.Mode}
	def, ok := c.Lookup(key.Provider, key.Mode)
	if !ok {
		return Definition{}, Key{}, fmt.Errorf(
			"channel %s/%s is not installed", key.Provider, key.Mode)
	}
	return def, key, nil
}
