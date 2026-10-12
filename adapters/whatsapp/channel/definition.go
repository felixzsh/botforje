package channel

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
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
// the presence the channel applies and the import and hand-over switches that
// used to live in the common settings block (ADR 0054).
type BotOptions struct {
	Settings       Settings
	History        bool
	ImportContacts bool
	// ExternalReplyPause is the per-chat hand-over window; zero uses the
	// fleet's default.
	ExternalReplyPause time.Duration
}

// rawOptions is the authored shape of channel.options, and of the legacy
// settings keys the definition still accepts. Pointers distinguish an absent
// key from an explicit zero, so a conflict between the two authored locations
// is detectable (ADR 0054).
type rawOptions struct {
	ReadReceipts       *bool `yaml:"read_receipts" json:"read_receipts,omitempty"`
	ReadReceiptDelay   *int  `yaml:"read_receipt_delay" json:"read_receipt_delay,omitempty"`
	ReplyDelay         *int  `yaml:"reply_delay" json:"reply_delay,omitempty"`
	SimulateTyping     *bool `yaml:"simulate_typing" json:"simulate_typing,omitempty"`
	HistorySync        *bool `yaml:"history_sync" json:"history_sync,omitempty"`
	ImportContacts     *bool `yaml:"import_contacts" json:"import_contacts,omitempty"`
	ExternalReplyPause *int  `yaml:"external_reply_pause" json:"external_reply_pause,omitempty"`
}

// legacySettings are the settings keys this adapter still reads from the
// common block, so an existing WhatsApp document keeps loading unchanged
// (ADR 0054).
var legacySettings = []string{
	"read_receipts", "read_receipt_delay", "reply_delay", "simulate_typing",
	"history_sync", "import_contacts", "external_reply_pause",
}

// Definition is the pure declaration of this adapter: no device store opens
// and no transport starts while config commands validate against it (ADR 0052).
func Definition() channels.Definition {
	return channels.Definition{
		Provider: Provider,
		Kind:     "whatsmeow",
		Modes:    []string{Mode},
		Capabilities: channels.Capabilities{
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
		},
		OptionsSchema:   optionsSchema(),
		Decode:          decodeBot,
		LegacySettings:  legacySettings,
		TemplateAliases: templateAliases(),
	}
}

// templateAliases is the WhatsApp PN compatibility table: an old reference maps
// to the neutral peer path that means the same, and contact.phone maps to
// peer.subject only because the native account mode addresses by PN
// (ADR 0018/0020). Every other adapter does not inherit these aliases.
func templateAliases() map[string]string {
	return map[string]string{
		"contact.id":    "peer.id",
		"contact.name":  "peer.name",
		"contact.phone": "peer.subject",
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

// decodeBot reads the authored options and legacy settings into BotOptions,
// reporting every value the adapter cannot accept (ADR 0052).
func decodeBot(bot spec.Bot) (any, []string) {
	var authored *yaml.Node
	if bot.Channel != nil {
		authored = bot.Channel.Options.Node()
	}
	fromOptions, reasons := decodeNode(authored, "channel.options")
	fromSettings, settingsReasons := decodeLegacy(bot.Settings)
	reasons = append(reasons, settingsReasons...)
	merged, mergeReasons := mergeOptions(fromOptions, fromSettings)
	reasons = append(reasons, mergeReasons...)
	reasons = append(reasons, checkRanges(merged)...)
	common := spec.BotSettings{}
	if bot.Settings != nil {
		common = *bot.Settings
	}
	return builtOptions(merged, common), reasons
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

// decodeLegacy decodes the adapter-owned keys of the common settings block.
func decodeLegacy(settings *spec.BotSettings) (rawOptions, []string) {
	var raw rawOptions
	if settings == nil || len(settings.Adapter) == 0 {
		return raw, nil
	}
	data, err := yaml.Marshal(settings.Adapter)
	if err != nil {
		return raw, []string{fmt.Sprintf("settings: %v", err)}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return raw, []string{fmt.Sprintf("settings: %v", err)}
	}
	return raw, nil
}

// mergeOptions folds the legacy settings into the authored options, rejecting
// a key the author declared in both places.
func mergeOptions(options, legacy rawOptions) (rawOptions, []string) {
	var reasons []string
	o, l := &options, &legacy
	o.ReadReceipts = mergeOption("read_receipts", o.ReadReceipts, l.ReadReceipts, &reasons)
	o.ReadReceiptDelay = mergeOption(
		"read_receipt_delay", o.ReadReceiptDelay, l.ReadReceiptDelay, &reasons)
	o.ReplyDelay = mergeOption("reply_delay", o.ReplyDelay, l.ReplyDelay, &reasons)
	o.SimulateTyping = mergeOption(
		"simulate_typing", o.SimulateTyping, l.SimulateTyping, &reasons)
	o.HistorySync = mergeOption("history_sync", o.HistorySync, l.HistorySync, &reasons)
	o.ImportContacts = mergeOption(
		"import_contacts", o.ImportContacts, l.ImportContacts, &reasons)
	o.ExternalReplyPause = mergeOption(
		"external_reply_pause", o.ExternalReplyPause, l.ExternalReplyPause, &reasons)
	return *o, reasons
}

// mergeOption keeps the authored options value, falls back to the legacy
// settings key, and reports a key written in both places.
func mergeOption[T any](name string, options, legacy *T, reasons *[]string) *T {
	switch {
	case options != nil && legacy != nil:
		*reasons = append(*reasons,
			name+" is declared in both channel.options and settings")
		return options
	case options != nil:
		return options
	default:
		return legacy
	}
}

func checkRanges(raw rawOptions) []string {
	var reasons []string
	for _, value := range []struct {
		name string
		at   *int
	}{
		{"read_receipt_delay", raw.ReadReceiptDelay},
		{"reply_delay", raw.ReplyDelay},
		{"external_reply_pause", raw.ExternalReplyPause},
	} {
		if value.at != nil && *value.at < 0 {
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
	if raw.ReadReceiptDelay != nil {
		options.Settings.ReadReceiptDelay = time.Duration(*raw.ReadReceiptDelay) * time.Millisecond
	}
	if raw.ReplyDelay != nil {
		options.Settings.ReplyDelay = time.Duration(*raw.ReplyDelay) * time.Millisecond
	}
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
	if raw.ExternalReplyPause != nil {
		options.ExternalReplyPause = time.Duration(*raw.ExternalReplyPause) * time.Millisecond
	}
	return options
}

// Prepare returns the definition's readiness hook bound to one installation's
// dependencies: a bot joins the fleet only once WhatsApp confirms a usable link,
// and a residual or missing pairing skips it without stopping its peers
// (ADR 0022, ADR 0053).
func Prepare(deps Deps) func(context.Context, channels.Bind) (bool, string, error) {
	return func(ctx context.Context, bind channels.Bind) (bool, string, error) {
		state, err := whatsapp.CheckPairing(
			ctx, deps.Devices, deps.Bindings, deps.Pairings, bind.Scope, bind.Bot)
		if err != nil {
			return false, "", err
		}
		return state == botflota.PairingPaired, string(state), nil
	}
}

// Deps are the process-wide dependencies one WhatsApp installation needs; the
// definition's builder closes over them, so the shared fleet code never sees a
// device store (ADR 0052, ADR 0054).
type Deps struct {
	Devices    *whatsapp.Devices
	Bindings   botflota.BindingStore
	Pairings   botflota.PairingStore
	Peers      botflota.PeerStore
	Chats      botflota.ChatStore
	Messages   botflota.MessageStore
	Directory  botflota.DirectoryStore
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
			Pairings:       deps.Pairings,
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
