package channel

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// Provider and Mode are this adapter's catalog identity (ADR 0054).
const (
	Provider = "whatsapp"
	Mode     = "native"
)

// BotOptions is the decoded adapter-owned configuration of one WhatsApp bot:
// the presence the channel applies, its import switches, and its companion
// device name (ADR 0054).
type BotOptions struct {
	Settings       Settings
	History        bool
	ImportContacts bool
	// DeviceName is the companion name WhatsApp shows in Linked devices. It is
	// read only while registering; an existing link keeps its name, and empty
	// uses the adapter's built-in default (ADR 0053).
	DeviceName string
}

// rawOptions is the authored shape of channel.options. A pointer distinguishes
// an absent switch from an explicit false, so the adapter's own defaults apply
// only where the author said nothing (ADR 0054).
type rawOptions struct {
	ReadReceipts     *bool  `yaml:"read_receipts" json:"read_receipts,omitempty"`
	ReadReceiptDelay int    `yaml:"read_receipt_delay" json:"read_receipt_delay,omitempty"`
	ReplyDelay       int    `yaml:"reply_delay" json:"reply_delay,omitempty"`
	SimulateTyping   *bool  `yaml:"simulate_typing" json:"simulate_typing,omitempty"`
	HistorySync      *bool  `yaml:"history_sync" json:"history_sync,omitempty"`
	ImportContacts   *bool  `yaml:"import_contacts" json:"import_contacts,omitempty"`
	DeviceName       string `yaml:"device_name" json:"device_name,omitempty"`
}

// Definition is the pure declaration of this adapter: no device store opens
// and no transport starts while config commands validate against it (ADR 0052).
func Definition() channels.Definition {
	caps := channels.Capabilities{
		Send: []botflota.MessageKind{
			botflota.KindText,
			botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
			botflota.KindDocument, botflota.KindSticker,
			botflota.KindLocation, botflota.KindContact, botflota.KindPoll,
		},
		Receive: []botflota.MessageKind{
			botflota.KindText,
			botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
			botflota.KindDocument, botflota.KindSticker,
			botflota.KindLocation, botflota.KindContact, botflota.KindPoll,
			botflota.KindEvent, botflota.KindInteractive,
			botflota.KindCommerce, botflota.KindCall, botflota.KindUnknown,
		},
		Features: []string{
			channels.FeaturePresence,
			channels.FeatureHistoryImport,
			channels.FeatureAddressBookImport,
			channels.FeatureExternalReply,
		},
		CardVariants: []spec.ContactEntryKind{
			spec.ContactPhone, spec.ContactEmail,
			spec.ContactAddress, spec.ContactURL,
		},
	}
	return channels.Definition{
		Provider:       Provider,
		Kind:           "whatsmeow",
		Modes:          []string{Mode},
		Capabilities:   caps,
		OptionsSchema:  optionsSchema,
		Decode:         decodeBot,
		ValidateTarget: validateTarget,
		// WhatsApp identities are provider-wide: the binding carries no
		// identity-space context by design, so compatibility is the same
		// provider and account mode (ADR 0042, ADR 0051).
		CompatibleBindings: compatibleBindings,
		// A provider id is interpreted under the binding that
		// recorded it: a second account is a different space, while
		// reauthorization of the same account keeps it and a binding
		// generation change never changes it by itself (ADR 0026,
		// ADR 0051). The value is the binding's own subject, opaque to
		// shared code.
		MessageSpace: messageSpace,
		SetupMethods: []string{whatsapp.SetupMethodQR},
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
	reasons = append(reasons, checkRanges(raw)...)
	common := spec.BotSettings{}
	if bot.Settings != nil {
		common = *bot.Settings
	}
	return builtOptions(raw, common), reasons
}

// validateTarget states what this implementation can address: its own
// provider, direct conversations only, and no topic. A shared conversation
// or a topic is refused rather than sent somewhere the target does not name
// (ADR 0044, ADR 0051). It mirrors the delivery-time thread rules so
// admission and delivery agree.
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

// messageSpace reports the message-ID space of one committed binding:
// the binding's own subject. It is pure and adapter-owned: shared
// code threads the opaque value without parsing it. A binding of another
// provider carries no space this adapter can interpret.
func messageSpace(binding *botflota.Binding) string {
	if binding == nil || binding.Identity.Provider != Provider {
		return ""
	}
	return binding.Identity.Subject
}

// compatibleBindings reports why one bot's history cannot move to another
// bot's binding. WhatsApp identities are provider-wide with no
// identity-space context, so the same provider and account mode share the
// space and anything else does not. It is pure: subjects stay opaque and
// are never parsed (ADR 0042, ADR 0051).
func compatibleBindings(from, to botflota.Binding) []string {
	if from.Provider != Provider {
		return []string{fmt.Sprintf(
			"source binding provider %q is not %s", from.Provider, Provider)}
	}
	if to.Provider != Provider {
		return []string{fmt.Sprintf(
			"destination binding provider %q is not %s", to.Provider, Provider)}
	}
	if from.Mode != to.Mode {
		return []string{fmt.Sprintf(
			"account mode %q cannot receive history from mode %q",
			to.Mode, from.Mode)}
	}
	return nil
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

func checkRanges(raw rawOptions) []string {
	var reasons []string
	for _, value := range []struct {
		name string
		at   int
	}{
		{"read_receipt_delay", raw.ReadReceiptDelay},
		{"reply_delay", raw.ReplyDelay},
	} {
		if value.at < 0 {
			reasons = append(reasons, value.name+" must be >= 0")
		}
	}
	return reasons
}

// builtOptions resolves the decoded values and the adapter's defaults. History
// import defaults on and address-book import off for WhatsApp only, never as a
// shared default (ADR 0054).
func builtOptions(raw rawOptions, common spec.BotSettings) BotOptions {
	options := BotOptions{History: true}
	if raw.ReadReceipts != nil {
		options.Settings.ReadReceipts = *raw.ReadReceipts
	}
	options.Settings.ReadReceiptDelay =
		time.Duration(raw.ReadReceiptDelay) * time.Millisecond
	options.Settings.ReplyDelay = time.Duration(raw.ReplyDelay) * time.Millisecond
	if raw.SimulateTyping != nil {
		options.Settings.SimulateTyping = *raw.SimulateTyping
	}
	options.Settings.Jitter = time.Duration(common.Jitter) * time.Millisecond
	if raw.HistorySync != nil {
		options.History = *raw.HistorySync
	}
	if raw.ImportContacts != nil {
		options.ImportContacts = *raw.ImportContacts
	}
	options.DeviceName = raw.DeviceName
	return options
}

// Prepare returns the definition's readiness hook bound to one installation's
// dependencies: a bot joins the fleet only once WhatsApp confirms a usable link,
// and a residual or missing pairing skips it without stopping its peers
// (ADR 0022, ADR 0053). Readiness is computed from the committed binding and
// the device store, never from a stored status row.
func Prepare(deps Deps) func(context.Context, channels.Bind) (bool, string, error) {
	return func(ctx context.Context, bind channels.Bind) (bool, string, error) {
		if deps.Devices == nil || deps.Bindings == nil {
			return false, "", fmt.Errorf("whatsapp: no device or binding store")
		}
		owner := whatsapp.AccountOwner{Devices: deps.Devices, Bindings: deps.Bindings}
		status, err := owner.InspectBinding(ctx, bind.Scope, bind.Bot)
		if err != nil {
			return false, "", err
		}
		return status.Condition == botflota.ReadinessConfigured,
			strings.Join(status.Reasons, "; "), nil
	}
}

// Setup returns one bot's neutral account lifecycle and setup runner over
// the installation's stores: inspection reads the committed binding and the
// device store, detachment settles the queue before it forgets the link, and
// the pairing manager speaks the neutral session contract (ADR 0053). A nil
// Pairs leaves detachment without a setup wait; every other nil dependency
// is an error, never an invented state.
func Setup(deps Deps) func(channels.Bind) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
	return func(bind channels.Bind) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
		if deps.Devices == nil {
			return nil, nil, fmt.Errorf("whatsapp: no device store")
		}
		if deps.Bindings == nil {
			return nil, nil, fmt.Errorf("whatsapp: no binding store")
		}
		if deps.Outbox == nil {
			return nil, nil, fmt.Errorf("whatsapp: no outbox store")
		}
		if deps.Pairs == nil {
			return nil, nil, fmt.Errorf("whatsapp: no setup runner")
		}
		owner := whatsapp.AccountOwner{
			Devices: deps.Devices, Bindings: deps.Bindings,
			Outbox: deps.Outbox, Pairs: deps.Pairs,
		}
		return owner, deps.Pairs, nil
	}
}

// Deps are the process-wide dependencies one WhatsApp installation needs; the
// definition's builder closes over them, so the shared fleet code never sees a
// device store (ADR 0052, ADR 0054).
type Deps struct {
	Devices    *whatsapp.Devices
	Bindings   botflota.BindingStore
	Peers      botflota.PeerStore
	Chats      botflota.ChatStore
	Messages   botflota.MessageStore
	Directory  botflota.DirectoryStore
	Outbox     botflota.OutboxStore
	Pairs      *whatsapp.PairManager
	HTTPClient *http.Client
	// DepsLogLevel is the minimum level for forwarded whatsmeow logs.
	DepsLogLevel slog.Level
	Queue        int
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	Now          func() time.Time
	Rand         *rand.Rand
}

// Builder returns the definition's builder bound to one installation's
// dependencies; each call fills the per-bot values from the bind.
func Builder(deps Deps) func(channels.Bind) (botflota.MessageChannel, error) {
	return func(bind channels.Bind) (botflota.MessageChannel, error) {
		options, ok := bind.Options.(BotOptions)
		if !ok {
			return nil, fmt.Errorf("whatsapp: unexpected options %T", bind.Options)
		}
		return New(Options{
			Scope:          bind.Scope,
			Bot:            bind.Bot,
			Devices:        deps.Devices,
			Bindings:       deps.Bindings,
			Peers:          deps.Peers,
			Chats:          deps.Chats,
			Messages:       deps.Messages,
			History:        options.History,
			Directory:      deps.Directory,
			ImportContacts: options.ImportContacts,
			HTTPClient:     deps.HTTPClient,
			Settings:       options.Settings,
			Log:            bind.Log,
			DepsLogLevel:   deps.DepsLogLevel,
			Queue:          deps.Queue,
			MinBackoff:     deps.MinBackoff,
			MaxBackoff:     deps.MaxBackoff,
			Now:            deps.Now,
			Rand:           deps.Rand,
		})
	}
}
