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
    external_reply_pause: 1000
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
	if options.ExternalReplyPause != time.Second {
		t.Errorf("external pause = %s", options.ExternalReplyPause)
	}
}

func TestDecodeBotStillReadsLegacySettings(t *testing.T) {
	options, reasons := decodeYAML(t, `graph: menu
settings:
  read_receipts: true
  reply_delay: 900
  history_sync: false
  import_contacts: true
`)
	if len(reasons) != 0 {
		t.Fatalf("reasons = %v", reasons)
	}
	if !options.Settings.ReadReceipts || options.Settings.ReplyDelay != 900*time.Millisecond {
		t.Errorf("legacy presence not mapped: %#v", options.Settings)
	}
	if options.History || !options.ImportContacts {
		t.Errorf("legacy import switches not mapped: %#v", options)
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
			name: "declared twice",
			doc: `graph: menu
channel:
  provider: whatsapp
  mode: native
  options:
    reply_delay: 900
settings:
  reply_delay: 500
`,
			want: "declared in both",
		},
		{
			name: "negative delay",
			doc: `graph: menu
settings:
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
	if def.OptionsSchema == nil {
		t.Fatal("options schema is missing")
	}
	if len(def.LegacySettings) == 0 {
		t.Fatal("legacy settings are not declared")
	}
}
