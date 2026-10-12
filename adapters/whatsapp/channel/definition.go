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

// rawOptions is the authored shape of channel.options. A pointer distinguishes
// an absent switch from an explicit false, so the adapter's own defaults apply
// only where the author said nothing (ADR 0054).
type rawOptions struct {
	ReadReceipts       *bool `yaml:"read_receipts" json:"read_receipts,omitempty"`
	ReadReceiptDelay   int   `yaml:"read_receipt_delay" json:"read_receipt_delay,omitempty"`
	ReplyDelay         int   `yaml:"reply_delay" json:"reply_delay,omitempty"`
	SimulateTyping     *bool `yaml:"simulate_typing" json:"simulate_typing,omitempty"`
	HistorySync        *bool `yaml:"history_sync" json:"history_sync,omitempty"`
	ImportContacts     *bool `yaml:"import_contacts" json:"import_contacts,omitempty"`
	ExternalReplyPause int   `yaml:"external_reply_pause" json:"external_reply_pause,omitempty"`
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
		OptionsSchema: optionsSchema(),
		Decode:        decodeBot,
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
		{"external_reply_pause", raw.ExternalReplyPause},
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
	options.ExternalReplyPause =
		time.Duration(raw.ExternalReplyPause) * time.Millisecond
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
