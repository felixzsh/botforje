package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// channelOptions builds an authored options block for a test bot.
func channelOptions(t *testing.T, body string) spec.ChannelOptions {
	t.Helper()
	indented := strings.ReplaceAll(strings.TrimSpace(body), "\n", "\n  ")
	document := "options:\n  " + indented + "\n"
	var parsed spec.Channel
	if err := yaml.Unmarshal([]byte(document), &parsed); err != nil {
		t.Fatalf("channel options: %v", err)
	}
	return parsed.Options
}

// strictOptions is a test definition's pure options decoder: it accepts the
// listed keys and reports every other one, the way a real adapter's does
// (ADR 0052).
func strictOptions(allowed ...string) func(spec.Bot) (any, []string) {
	known := map[string]bool{}
	for _, key := range allowed {
		known[key] = true
	}
	return func(bot spec.Bot) (any, []string) {
		if bot.Channel == nil || bot.Channel.Options.IsZero() {
			return nil, nil
		}
		var values map[string]any
		if err := bot.Channel.Options.Node().Decode(&values); err != nil {
			return nil, []string{fmt.Sprintf("channel options: %v", err)}
		}
		var reasons []string
		for _, key := range SortedKeys(values) {
			if !known[key] {
				reasons = append(reasons, fmt.Sprintf("unknown option %q", key))
			}
		}
		return values, reasons
	}
}

// capabilityCatalog installs a capable WhatsApp definition and a text-only fake,
// so validation can be exercised per consuming bot (ADR 0052).
func capabilityCatalog(t *testing.T) *channels.Catalog {
	t.Helper()
	catalog, err := channels.New(
		channels.Definition{
			Provider: "whatsapp", Kind: "whatsapp", Modes: []string{"native"},
			Capabilities: channels.Capabilities{
				Send: []botflota.MessageKind{
					botflota.KindText,
					botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
					botflota.KindDocument, botflota.KindSticker,
					botflota.KindLocation, botflota.KindContact, botflota.KindPoll,
				},
				Features: []string{
					channels.FeaturePresence,
					channels.FeatureHistoryImport,
					channels.FeatureAddressBookImport,
					channels.FeatureExternalReply,
				},
			},
			Decode:         strictOptions("read_receipts", "history_sync"),
			LegacySettings: []string{"read_receipts", "history_sync"},
			TemplateAliases: map[string]string{
				"contact.id":    "peer.id",
				"contact.name":  "peer.name",
				"contact.phone": "peer.subject",
			},
		},
		channels.Definition{
			Provider: "fake", Kind: "fake", Modes: []string{"text"},
			Capabilities: channels.Capabilities{
				Send: []botflota.MessageKind{botflota.KindText},
			},
			Decode: strictOptions(),
		},
		channels.Definition{
			Provider: "fake", Kind: "fake", Modes: []string{"rich"},
			Capabilities: channels.Capabilities{
				Send: []botflota.MessageKind{botflota.KindText, botflota.KindImage},
			},
			Decode: strictOptions(),
		},
	)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return catalog
}

// botConfig builds one bot whose graph runs the given action steps.
func botConfig(bot spec.Bot, steps []spec.Step) *Config {
	if steps == nil {
		steps = []spec.Step{{Message: &spec.Message{Body: spec.Text{"hi"}}}}
	}
	return &Config{Scopes: map[string]spec.Scope{
		"dev": {
			Actions: map[string]spec.Action{"greet": {Steps: steps}},
			Graphs: map[string]spec.Graph{"menu": {
				Root: "root",
				Nodes: map[string]spec.Node{
					"root": {OnEnter: []string{"greet"}},
				},
			}},
			Bots: map[string]spec.Bot{"demo": bot},
		},
	}}
}

func TestChannelSelectionAndCapabilityValidation(t *testing.T) {
	imageStep := []spec.Step{{Media: &spec.Media{
		Kind: "image", URL: "https://example.test/a.png",
	}}}
	cases := []struct {
		name  string
		bot   spec.Bot
		steps []spec.Step
		want  []string
	}{
		{
			name: "missing selector defaults to WhatsApp native",
			bot:  spec.Bot{Graph: "menu"},
		},
		{
			name: "explicit text-only fake accepts text",
			bot: spec.Bot{Graph: "menu", Channel: &spec.Channel{
				Provider: "fake", Mode: "text",
			}},
		},
		{
			name: "text-only fake refuses media",
			bot: spec.Bot{Graph: "menu", Channel: &spec.Channel{
				Provider: "fake", Mode: "text",
			}},
			steps: imageStep,
			want:  []string{`channel fake/text cannot send image`},
		},
		{
			name: "unavailable provider never falls back",
			bot: spec.Bot{Graph: "menu", Channel: &spec.Channel{
				Provider: "telegram", Mode: "bot",
			}},
			want: []string{"not installed"},
		},
		{
			name: "unknown option field fails with context",
			bot: spec.Bot{Graph: "menu", Channel: &spec.Channel{
				Provider: "whatsapp", Mode: "native",
				Options: channelOptions(t, "nope: true"),
			}},
			want: []string{`unknown option "nope"`},
		},
		{
			name: "legacy setting stays valid for WhatsApp",
			bot: spec.Bot{Graph: "menu", Settings: &spec.BotSettings{
				Adapter: map[string]any{"read_receipts": true},
			}},
		},
		{
			name: "legacy setting fails for another channel",
			bot: spec.Bot{Graph: "menu",
				Channel: &spec.Channel{Provider: "fake", Mode: "text"},
				Settings: &spec.BotSettings{
					Adapter: map[string]any{"read_receipts": true},
				}},
			want: []string{`setting "read_receipts" is not supported by channel fake/text`},
		},
		{
			name: "external reply pause needs the observation feature",
			bot: spec.Bot{Graph: "menu",
				Channel: &spec.Channel{Provider: "fake", Mode: "text"},
				Settings: &spec.BotSettings{
					ExternalReplyPause: 1000,
				}},
			want: []string{"observe external replies"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := Validate(botConfig(tc.bot, tc.steps), capabilityCatalog(t))
			for _, want := range tc.want {
				found := false
				for _, e := range errs {
					if strings.Contains(e.Error(), want) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("no problem mentions %q; got %v", want, errs)
				}
			}
			if len(tc.want) == 0 && len(errs) != 0 {
				t.Fatalf("want valid, got %v", errs)
			}
		})
	}
}

func TestChannelSchemaMatchesTheInstalledSelection(t *testing.T) {
	data, err := JSONSchema(capabilityCatalog(t))
	if err != nil {
		t.Fatalf("JSONSchema: %v", err)
	}
	schema := string(data)
	for _, want := range []string{
		`"const": "whatsapp"`, `"const": "native"`,
		`"const": "fake"`, `"const": "text"`,
		`"read_receipts"`, `"history_sync"`,
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema is missing %s", want)
		}
	}
	if strings.Contains(schema, "ADR") {
		t.Error("the editor schema must not mention design notes")
	}
}

// TestSharedActionCheckedPerConsumingBot proves one shared action is checked in
// the context of each consuming bot: the same graph fits a richer fake and
// fails for the text-only one, naming the bot, action, and step (ADR 0052).
func TestSharedActionCheckedPerConsumingBot(t *testing.T) {
	imageStep := []spec.Step{{Media: &spec.Media{
		Kind: "image", URL: "https://example.test/a.png",
	}}}
	withBot := func(bot spec.Bot) *Config {
		cfg := botConfig(bot, imageStep)
		cfg.Scopes["dev"].Bots["text"] = spec.Bot{
			Graph:   "menu",
			Channel: &spec.Channel{Provider: "fake", Mode: "text"},
		}
		return cfg
	}
	rich := spec.Bot{
		Graph:   "menu",
		Channel: &spec.Channel{Provider: "fake", Mode: "rich"},
	}
	text := spec.Bot{
		Graph:   "menu",
		Channel: &spec.Channel{Provider: "fake", Mode: "text"},
	}

	t.Run("capable bot alone", func(t *testing.T) {
		cfg := botConfig(rich, imageStep)
		if errs := Validate(cfg, capabilityCatalog(t)); len(errs) != 0 {
			t.Fatalf("want valid, got %v", errs)
		}
	})
	t.Run("text-only bot alone", func(t *testing.T) {
		cfg := botConfig(text, imageStep)
		errs := Validate(cfg, capabilityCatalog(t))
		if !mentions(errs, `action "greet" step 0: channel fake/text cannot send image`) {
			t.Fatalf("want the step and action in context, got %v", errs)
		}
	})
	t.Run("both sharing one graph", func(t *testing.T) {
		cfg := withBot(rich)
		delete(cfg.Scopes["dev"].Bots, "demo")
		errs := Validate(cfg, capabilityCatalog(t))
		if len(errs) != 1 || errs[0].Entity != `bot "text" (scope "dev")` {
			t.Fatalf("want exactly the text bot's problem, got %v", errs)
		}
	})
}

func mentions(errs []ValidationError, want string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), want) {
			return true
		}
	}
	return false
}

// TestOldWhatsappYamlStillLoads covers the compatibility half of the transition:
// a document written before the selector existed keeps loading and validating,
// with its adapter-owned keys preserved as authored (ADR 0054).
func TestOldWhatsappYamlStillLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	content := `scopes:
  dev:
    actions:
      greet:
        steps:
          - message:
              body: hi
    graphs:
      menu:
        root: root
        nodes:
          root:
            on_enter: [greet]
    bots:
      demo:
        graph: menu
        settings:
          read_receipts: true
          reply_delay: 900
          history_sync: false
          import_contacts: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := (Loader{}).Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if errs := Validate(cfg, testCatalog(t)); len(errs) != 0 {
		t.Fatalf("old YAML must validate, got %v", errs)
	}
	settings := cfg.Scopes["dev"].Bots["demo"].Settings
	if settings == nil || settings.Adapter["read_receipts"] != true {
		t.Fatalf("legacy settings not preserved: %#v", settings)
	}
}

// TestLegacyTemplateAliasCheckedPerConsumingBot proves the P1 translation is
// applied per consuming bot: the WhatsApp definition maps contact.phone to the
// neutral peer path, and a channel that owns no such alias refuses it with the
// bot, graph, node, and action that carry the reference (ADR 0018/0020).
func TestLegacyTemplateAliasCheckedPerConsumingBot(t *testing.T) {
	steps := []spec.Step{{Message: &spec.Message{
		Body: spec.Text{"Phone: {{contact.phone}}"},
	}}}
	whatsapp := botConfig(spec.Bot{Graph: "menu"}, steps)
	if errs := Validate(whatsapp, capabilityCatalog(t)); len(errs) != 0 {
		t.Fatalf("WhatsApp must map the alias, got %v", errs)
	}

	fake := botConfig(spec.Bot{
		Graph:   "menu",
		Channel: &spec.Channel{Provider: "fake", Mode: "text"},
	}, steps)
	errs := Validate(fake, capabilityCatalog(t))
	want := `template "contact.phone" is not supported by channel fake/text`
	if !mentions(errs, want) {
		t.Fatalf("no problem mentions %q; got %v", want, errs)
	}
	if !mentions(errs, `graph "menu" node "root": action "greet" action step 0`) {
		t.Fatalf("the problem lacks bot/action context: %v", errs)
	}

	// An alias no installed definition declares still fails at scope level, so
	// an action no bot reaches cannot hide a broken reference.
	orphan := botConfig(spec.Bot{Graph: "menu"}, []spec.Step{{Message: &spec.Message{
		Body: spec.Text{"{{contact.fax}}"},
	}}})
	if !mentions(Validate(orphan, capabilityCatalog(t)), "unknown root") {
		t.Fatal("an undeclared legacy path must fail validation")
	}
}
