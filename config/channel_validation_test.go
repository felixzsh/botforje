package config

import (
	"encoding/json"
	"fmt"
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
		if bot.Channel.Options.IsZero() {
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
				CardVariants: []spec.ContactEntryKind{
					spec.ContactPhone, spec.ContactEmail,
					spec.ContactAddress, spec.ContactURL,
				},
			},
			Decode: strictOptions("read_receipts", "history_sync"),
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

// defaultChannel is the selector a test bot uses when the case does not test
// the selector itself.
func defaultChannel() spec.Channel {
	return spec.Channel{Provider: "whatsapp", Mode: "native"}
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
			name: "named implementation accepts text",
			bot:  spec.Bot{Graph: "menu", Channel: defaultChannel()},
		},
		{
			name: "missing selector fails",
			bot:  spec.Bot{Graph: "menu"},
			want: []string{"provider is required"},
		},
		{
			name: "explicit text-only fake accepts text",
			bot: spec.Bot{Graph: "menu", Channel: spec.Channel{
				Provider: "fake", Mode: "text",
			}},
		},
		{
			name: "text-only fake refuses media",
			bot: spec.Bot{Graph: "menu", Channel: spec.Channel{
				Provider: "fake", Mode: "text",
			}},
			steps: imageStep,
			want:  []string{`channel fake/text cannot send image`},
		},
		{
			name: "unavailable provider never falls back",
			bot: spec.Bot{Graph: "menu", Channel: spec.Channel{
				Provider: "telegram", Mode: "bot",
			}},
			want: []string{"not installed"},
		},
		{
			name: "unknown option field fails with context",
			bot: spec.Bot{Graph: "menu", Channel: spec.Channel{
				Provider: "whatsapp", Mode: "native",
				Options: channelOptions(t, "nope: true"),
			}},
			want: []string{`unknown option "nope"`},
		},
		{
			name: "external reply pause needs the observation feature",
			bot: spec.Bot{Graph: "menu",
				Channel: spec.Channel{Provider: "fake", Mode: "text"},
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
			Channel: spec.Channel{Provider: "fake", Mode: "text"},
		}
		return cfg
	}
	rich := spec.Bot{
		Graph:   "menu",
		Channel: spec.Channel{Provider: "fake", Mode: "rich"},
	}
	text := spec.Bot{
		Graph:   "menu",
		Channel: spec.Channel{Provider: "fake", Mode: "text"},
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

// TestContactCardVariantCheckedPerConsumingBot proves a card variant an adapter
// cannot compose fails validation for the bot that would send it, with the step
// and entry in context, instead of being dropped at delivery (ADR 0036, 0052).
func TestContactCardVariantCheckedPerConsumingBot(t *testing.T) {
	steps := []spec.Step{{Contact: &spec.Contact{
		Name: "Ana",
		Entries: []spec.ContactEntry{
			{Kind: "phone", Value: "+5215500000000"},
			{Kind: "url", Value: "https://example.test/ana"},
		},
	}}}
	whatsapp := botConfig(spec.Bot{Graph: "menu", Channel: defaultChannel()}, steps)
	if errs := Validate(whatsapp, capabilityCatalog(t)); len(errs) != 0 {
		t.Fatalf("WhatsApp composes every variant, got %v", errs)
	}

	fake := botConfig(spec.Bot{
		Graph:   "menu",
		Channel: spec.Channel{Provider: "fake", Mode: "text"},
	}, steps)
	errs := Validate(fake, capabilityCatalog(t))
	want := `action "greet" step 0: channel fake/text cannot compose contact entry kind "url"`
	if !mentions(errs, want) {
		t.Fatalf("no problem mentions %q; got %v", want, errs)
	}
}

// TestUnsupportedContentFoundBehindToolsAndGuards proves the per-bot check
// follows every path that reaches an action, not only a node's entry: a tool
// reference and a guard's on_blocked steps are checked too (ADR 0052).
func TestUnsupportedContentFoundBehindToolsAndGuards(t *testing.T) {
	image := []spec.Step{{Media: &spec.Media{
		Kind: "image", URL: "https://example.test/a.png",
	}}}
	textBot := spec.Bot{
		Graph:   "menu",
		Channel: spec.Channel{Provider: "fake", Mode: "text"},
	}

	toolCase := botConfig(textBot, nil)
	scope := toolCase.Scopes["dev"]
	scope.Actions["photo"] = spec.Action{Steps: image}
	graph := scope.Graphs["menu"]
	node := graph.Nodes["root"]
	node.Tools = []string{"photo"}
	graph.Nodes["root"] = node
	scope.Graphs["menu"] = graph
	toolCase.Scopes["dev"] = scope
	if !mentions(Validate(toolCase, capabilityCatalog(t)),
		`action "photo" step 0: channel fake/text cannot send image`) {
		t.Fatal("an unsupported kind behind a tool path was not reported")
	}

	guardCase := botConfig(textBot, nil)
	scope = guardCase.Scopes["dev"]
	scope.Actions["greet"] = spec.Action{
		Guards: &spec.Guards{Cooldown: &spec.Cooldown{
			Duration: 60, OnBlocked: image,
		}},
		Steps: []spec.Step{{Message: &spec.Message{Body: spec.Text{"hi"}}}},
	}
	guardCase.Scopes["dev"] = scope
	if !mentions(Validate(guardCase, capabilityCatalog(t)),
		`action "greet" cooldown.on_blocked step 0: channel fake/text cannot send image`) {
		t.Fatal("an unsupported kind behind a guard was not reported")
	}
}

// TestChannelSchemaHasOneBranchPerSelector guards the multi-mode case: one
// definition installed on two account modes yields exactly one applicable
// branch per selector, so `oneOf` stays exclusive (ADR 0054).
func TestChannelSchemaHasOneBranchPerSelector(t *testing.T) {
	catalog, err := channels.New(channels.Definition{
		Provider: "fake", Kind: "fake", Modes: []string{"a", "b"},
		Capabilities: channels.Capabilities{
			Send: []botflota.MessageKind{botflota.KindText},
		},
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	data, err := JSONSchema(catalog)
	if err != nil {
		t.Fatalf("JSONSchema: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("schema: %v", err)
	}
	defs, _ := root["$defs"].(map[string]any)
	channel, _ := defs["Channel"].(map[string]any)
	branches, _ := channel["oneOf"].([]any)
	seen := map[string]bool{}
	for _, branch := range branches {
		props, _ := branch.(map[string]any)["properties"].(map[string]any)
		provider, _ := props["provider"].(map[string]any)["const"].(string)
		mode, _ := props["mode"].(map[string]any)["const"].(string)
		if seen[provider+"/"+mode] {
			t.Fatalf("duplicate branch %s/%s", provider, mode)
		}
		seen[provider+"/"+mode] = true
	}
	if len(branches) != 2 || !seen["fake/a"] || !seen["fake/b"] {
		t.Fatalf("branches = %v", seen)
	}
}

// TestDefinitionContentValidatorIsUsedPerBot proves the extension point: a
// definition enforces its own provider-correct limits and config reports them
// with the consuming bot's context (ADR 0052). A definition without the hook
// falls back to its declared capabilities, covered above.
func TestDefinitionContentValidatorIsUsedPerBot(t *testing.T) {
	catalog, err := channels.New(channels.Definition{
		Provider: "fake", Kind: "fake", Modes: []string{"text"},
		Capabilities: channels.Capabilities{
			Send: []botflota.MessageKind{botflota.KindText},
		},
		ValidateContent: func(
			key channels.Key, actionName string, action spec.Action,
		) []string {
			var reasons []string
			for _, step := range action.Steps {
				if step.Message == nil {
					continue
				}
				for _, body := range step.Message.Body {
					if len(body) > 3 {
						reasons = append(reasons, fmt.Sprintf(
							"action %q: channel %s/%s limits a body to 3 characters",
							actionName, key.Provider, key.Mode))
					}
				}
			}
			return reasons
		},
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	cfg := botConfig(spec.Bot{
		Graph:   "menu",
		Channel: spec.Channel{Provider: "fake", Mode: "text"},
	}, []spec.Step{{Message: &spec.Message{Body: spec.Text{"toolong"}}}})
	errs := Validate(cfg, catalog)
	if !mentions(errs, "limits a body to 3 characters") {
		t.Fatalf("the definition's own limit was not enforced: %v", errs)
	}
}
