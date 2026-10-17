package channel

import (
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// decodeYAML decodes one authored bot and hands it to the definition, the way
// config validation and the fleet do.
func decodeYAML(t *testing.T, document string) (BotOptions, []string) {
	t.Helper()
	var bot spec.Bot
	if err := yaml.Unmarshal([]byte(document), &bot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	value, reasons := Definition().Decode(bot)
	options, ok := value.(BotOptions)
	if !ok {
		t.Fatalf("decoded %T, want BotOptions", value)
	}
	return options, reasons
}

func TestDecodeBotAppliesAdapterDefaults(t *testing.T) {
	options, reasons := decodeYAML(t, "graph: menu\n")
	if len(reasons) != 0 {
		t.Fatalf("reasons = %v", reasons)
	}
	if !options.History {
		t.Error("history import must default on for WhatsApp")
	}
	if options.ImportContacts {
		t.Error("address-book import must default off")
	}
	if options.Settings.ReadReceipts || options.Settings.SimulateTyping {
		t.Errorf("presence must default off: %#v", options.Settings)
	}
}

func TestDecodeBotReadsChannelOptions(t *testing.T) {
	options, reasons := decodeYAML(t, `graph: menu
channel:
  provider: whatsapp
  mode: native
  options:
    read_receipts: true
    read_receipt_delay: 700
    reply_delay: 900
    simulate_typing: true
    history_sync: false
    import_contacts: true
settings:
  jitter: 250
`)
	if len(reasons) != 0 {
		t.Fatalf("reasons = %v", reasons)
	}
	settings := options.Settings
	if !settings.ReadReceipts || !settings.SimulateTyping {
		t.Errorf("booleans not mapped: %#v", settings)
	}
	if settings.ReadReceiptDelay != 700*time.Millisecond ||
		settings.ReplyDelay != 900*time.Millisecond ||
		settings.Jitter != 250*time.Millisecond {
		t.Errorf("durations not mapped: %#v", settings)
	}
	if options.History {
		t.Error("history_sync: false must turn the import off")
	}
	if !options.ImportContacts {
		t.Error("import_contacts: true must turn the import on")
	}
}

func TestDecodeBotReportsWhatItCannotAccept(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "unknown option",
			doc: `graph: menu
channel:
  provider: whatsapp
  mode: native
  options:
    read_receipt: true
`,
			want: "channel.options",
		},
		{
			name: "negative delay",
			doc: `graph: menu
channel:
  provider: whatsapp
  mode: native
  options:
    reply_delay: -1
`,
			want: "must be >= 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, reasons := decodeYAML(t, tc.doc)
			if len(reasons) == 0 {
				t.Fatalf("want a reason mentioning %q", tc.want)
			}
			joined := strings.Join(reasons, "; ")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("reasons = %q, want %q", joined, tc.want)
			}
		})
	}
}

func TestDefinitionDeclaresItsCapabilities(t *testing.T) {
	def := Definition()
	if def.Provider != Provider || def.Modes[0] != Mode {
		t.Fatalf("identity = %s/%s", def.Provider, def.Modes[0])
	}
	for _, kind := range []botflota.MessageKind{
		botflota.KindText, botflota.KindImage,
		botflota.KindLocation, botflota.KindContact, botflota.KindPoll,
	} {
		if !def.Capabilities.SupportsSend(kind) {
			t.Errorf("send support is missing %s", kind)
		}
	}
	for _, feature := range []string{
		"history_import", "address_book_import", "external_reply_observation",
	} {
		if !def.Capabilities.HasFeature(feature) {
			t.Errorf("feature %s is not declared", feature)
		}
	}
	for _, kind := range []spec.ContactEntryKind{
		spec.ContactPhone, spec.ContactEmail, spec.ContactAddress, spec.ContactURL,
	} {
		if !def.Capabilities.SupportsCardVariant(kind) {
			t.Errorf("card variant %s is not declared", kind)
		}
	}
	if def.OptionsSchema == nil {
		t.Fatal("options schema is missing")
	}
}

// TestDecodeBotRejectsTheRetiredHandOverOption: the hand-over window is a shared
// fleet policy in `settings`, so the adapter no longer declares it as an option.
// Accepting it in two places would let one of them be silently ignored.
func TestDecodeBotRejectsTheRetiredHandOverOption(t *testing.T) {
	_, reasons := decodeYAML(t, `graph: menu
channel:
  provider: whatsapp
  mode: native
  options:
    external_reply_pause: 60000
`)
	if len(reasons) == 0 {
		t.Fatal("external_reply_pause must no longer be accepted as a channel option")
	}
	if !strings.Contains(strings.Join(reasons, "; "), "external_reply_pause") {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestDefinitionValidatesTargets(t *testing.T) {
	def := Definition()
	if def.ValidateTarget == nil {
		t.Fatal("ValidateTarget is nil, want the adapter's target rules")
	}
	direct := botflota.ChatTarget{
		Provider: Provider, Kind: botflota.ChatDirect, Thread: "5215500000000",
	}
	if reasons := def.ValidateTarget(direct); len(reasons) != 0 {
		t.Fatalf("direct target reasons = %v, want none", reasons)
	}
	cases := []struct {
		name   string
		target botflota.ChatTarget
	}{
		{"foreign provider", botflota.ChatTarget{
			Provider: "telegram", Kind: botflota.ChatDirect, Thread: "1",
		}},
		{"shared conversation", botflota.ChatTarget{
			Provider: Provider, Kind: botflota.ChatGroup, Thread: "g1",
		}},
		{"topic on a direct target", botflota.ChatTarget{
			Provider: Provider, Kind: botflota.ChatDirect,
			Thread: "5215500000000", Topic: "t",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if reasons := def.ValidateTarget(tc.target); len(reasons) == 0 {
				t.Fatalf("target %#v admitted, want reasons", tc.target)
			}
		})
	}
}

// TestDefinitionProvesBindingCompatibility: WhatsApp identities are
// provider-wide with no identity-space context, so the same provider and
// account mode share the space and anything else does not. Subjects stay
// opaque: the hook never parses them (ADR 0042, ADR 0051).
func TestDefinitionProvesBindingCompatibility(t *testing.T) {
	def := Definition()
	if def.CompatibleBindings == nil {
		t.Fatal("CompatibleBindings is nil, want the adapter's space rule")
	}
	binding := func(provider, mode string) botflota.Binding {
		return botflota.Binding{
			Scope: "support", Bot: "demo",
			Provider: provider, Adapter: "whatsmeow", Mode: mode,
			Identity: botflota.IdentityKey{
				Provider: provider, Subject: "5215500000000",
			},
		}
	}
	if reasons := def.CompatibleBindings(
		binding(Provider, Mode), binding(Provider, Mode),
	); len(reasons) != 0 {
		t.Fatalf("compatible reasons = %v, want none", reasons)
	}
	cases := []struct {
		name string
		from botflota.Binding
		to   botflota.Binding
	}{
		{"foreign source provider", binding("telegram", Mode), binding(Provider, Mode)},
		{"foreign destination provider", binding(Provider, Mode), binding("telegram", Mode)},
		{"different mode", binding(Provider, Mode), binding(Provider, "cloud")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if reasons := def.CompatibleBindings(tc.from, tc.to); len(reasons) == 0 {
				t.Fatalf("bindings %#v -> %#v admitted, want reasons", tc.from, tc.to)
			}
		})
	}
}
