// Package testchannel is an independent channel adapter used to prove that
// onboarding a channel is adapter work plus one registration (ADR 0052,
// ADR 0054). It is profile-local and text-only: a message id is unique within
// one committed account namespace, and it declares no optional operation it
// does not implement. It speaks no SDK, opens no network service, and needs
// no production credential, so its tests drive the real config, fleet,
// engine, stores, and API without a provider.
package testchannel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/invopop/jsonschema"
	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

const (
	// Provider is the catalog provider this adapter installs.
	Provider = "testchannel"
	// Mode is its only account mode. A second mode is a second declaration,
	// never a provider-wide assumption (ADR 0052).
	Mode = "local"
	// Kind is the implementation label the catalog exposes.
	Kind = "in-memory"
	// SetupMethodCredential links an account by handing over one opaque
	// credential through the neutral input step (ADR 0053).
	SetupMethodCredential = "credential"
	// SetupFieldCredential is the required input field of that step.
	SetupFieldCredential = "credential"
)

// BotOptions is the decoded adapter-owned configuration of one testchannel bot
// (ADR 0054).
type BotOptions struct {
	// Namespace is the profile-local identity-space context of this bot's
	// account: the same subject under two namespaces is still two spaces
	// (ADR 0026, ADR 0051).
	Namespace string
	// Label prefixes every delivered body, so a deployment can tell one
	// testchannel bot's deliveries apart.
	Label string
}

// rawOptions is the authored shape of channel.options. Namespace is required:
// a profile-local channel cannot promise message-id uniqueness without one.
type rawOptions struct {
	Namespace string `yaml:"namespace" json:"namespace" jsonschema:"required,minLength=1"`
	Label     string `yaml:"label" json:"label,omitempty"`
}

// Definition is the pure declaration of this adapter: nothing opens a store
// and nothing connects while config commands validate against it (ADR 0052).
func Definition() channels.Definition {
	return channels.Definition{
		Provider: Provider,
		Kind:     Kind,
		Modes:    []string{Mode},
		Capabilities: channels.Capabilities{
			Send:    []botflota.MessageKind{botflota.KindText},
			Receive: []botflota.MessageKind{botflota.KindText},
		},
		OptionsSchema:      optionsSchema,
		Decode:             decodeBot,
		ValidateTarget:     validateTarget,
		CompatibleBindings: compatibleBindings,
		// Message ids are local to the committed account namespace, so the
		// binding's own namespace is the space shared code threads
		// (ADR 0026, ADR 0051).
		MessageSpace: messageSpace,
		SetupMethods: []string{SetupMethodCredential},
	}
}

// optionsSchema is the editor schema of channel.options. It is strict:
// unknown keys are not part of the shape the runtime accepts (ADR 0054).
func optionsSchema() *jsonschema.Schema {
	reflector := &jsonschema.Reflector{
		Anonymous:                 true,
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	return reflector.Reflect(&rawOptions{})
}

// decodeBot reads the authored options into BotOptions, reporting every value
// the adapter cannot accept (ADR 0052).
func decodeBot(bot spec.Bot) (any, []string) {
	raw, reasons := decodeNode(bot.Channel.Options.Node(), "channel.options")
	if raw.Namespace == "" {
		reasons = append(reasons, "channel.options: namespace is required")
	}
	return BotOptions{Namespace: raw.Namespace, Label: raw.Label}, reasons
}

// decodeNode strictly decodes an authored node, so a misspelled option is
// reported instead of silently ignored.
func decodeNode(node *yaml.Node, where string) (rawOptions, []string) {
	var raw rawOptions
	if node == nil {
		return raw, nil
	}
	data, err := yaml.Marshal(node)
	if err != nil {
		return raw, []string{fmt.Sprintf("%s: %v", where, err)}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return raw, []string{fmt.Sprintf("%s: %v", where, err)}
	}
	return raw, nil
}

// validateTarget states what this implementation can address: its own
// provider, direct conversations only, and no topic (ADR 0044, ADR 0051).
func validateTarget(target botflota.ChatTarget) []string {
	if target.Provider != Provider {
		return []string{fmt.Sprintf(
			"target provider %q is not %s", target.Provider, Provider)}
	}
	if target.Kind != botflota.ChatDirect {
		return []string{"shared conversations are not supported"}
	}
	if target.Topic != "" {
		return []string{fmt.Sprintf(
			"direct target carries topic %q", target.Topic)}
	}
	return nil
}

// messageSpace reports the message-ID space of one committed binding: the
// binding's own profile-local namespace. Shared code threads the opaque value
// without parsing it (ADR 0026, ADR 0051).
func messageSpace(binding *botflota.Binding) string {
	if binding == nil || binding.Identity.Provider != Provider {
		return ""
	}
	return binding.Namespace
}

// compatibleBindings reports why one bot's history cannot move to another
// bot's binding: only the same provider, mode, and profile-local namespace
// share a space (ADR 0042, ADR 0051).
func compatibleBindings(from, to botflota.Binding) []string {
	if from.Provider != Provider || to.Provider != Provider {
		return []string{"both bindings must belong to " + Provider}
	}
	if from.Mode != to.Mode {
		return []string{fmt.Sprintf(
			"account mode %q cannot receive history from mode %q",
			to.Mode, from.Mode)}
	}
	if from.Namespace != to.Namespace {
		return []string{fmt.Sprintf(
			"namespace %q cannot receive history from namespace %q",
			to.Namespace, from.Namespace)}
	}
	return nil
}

// Deps are the process-wide dependencies one testchannel installation needs.
// The definition's builder closes over them, so the shared fleet never sees
// the in-memory backend (ADR 0052, ADR 0054).
type Deps struct {
	Bindings botflota.BindingStore
	// Backend is the shared in-memory provider a caller drives in tests; a
	// production installation constructs one per process.
	Backend *Backend
	Now     func() time.Time
}

// Builder returns the definition's builder bound to one installation's
// dependencies; each call fills the per-bot values from the bind.
func Builder(deps Deps) func(channels.Bind) (botflota.MessageChannel, error) {
	return func(bind channels.Bind) (botflota.MessageChannel, error) {
		options, ok := bind.Options.(BotOptions)
		if !ok {
			return nil, fmt.Errorf("testchannel: unexpected options %T", bind.Options)
		}
		if deps.Backend == nil {
			return nil, errors.New("testchannel: no in-memory backend")
		}
		return deps.Backend.newChannel(bind.Scope, bind.Bot, options), nil
	}
}

// Prepare reports whether one bot can join the fleet now: a committed binding
// of this provider is the only readiness this adapter needs (ADR 0022,
// ADR 0053). A definition without a binding store is always ready, which is
// what the config-only commands need.
func Prepare(deps Deps) func(context.Context, channels.Bind) (bool, string, error) {
	return func(ctx context.Context, bind channels.Bind) (bool, string, error) {
		if deps.Bindings == nil {
			return true, "", nil
		}
		binding, err := deps.Bindings.Get(ctx, bind.Scope, bind.Bot)
		if errors.Is(err, botflota.ErrNotFound) {
			return false, "no account linked", nil
		}
		if err != nil {
			return false, "", fmt.Errorf("testchannel: read binding: %w", err)
		}
		if binding.Identity.Provider != Provider {
			return false, "", fmt.Errorf(
				"testchannel: bot %q names provider %q",
				bind.Bot, binding.Identity.Provider)
		}
		return true, string(botflota.ReadinessConfigured), nil
	}
}

// Setup returns one bot's neutral account lifecycle and setup runner over the
// installation's binding store (ADR 0053).
func Setup(deps Deps) func(channels.Bind) (
	botflota.AccountLifecycle, botflota.SetupRunner, error,
) {
	return func(bind channels.Bind) (
		botflota.AccountLifecycle, botflota.SetupRunner, error,
	) {
		if deps.Bindings == nil {
			return nil, nil, errors.New("testchannel: no binding store")
		}
		options, ok := bind.Options.(BotOptions)
		if !ok {
			return nil, nil, fmt.Errorf("testchannel: unexpected options %T", bind.Options)
		}
		owner := AccountOwner{Bindings: deps.Bindings}
		runner := newRunner(deps.Bindings, options.Namespace, deps.Now)
		owner.Runner = runner
		return owner, runner, nil
	}
}
