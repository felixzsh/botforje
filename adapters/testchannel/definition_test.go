package testchannel_test

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/testchannel"
	"github.com/felixzsh/botflota/spec"
)

// TestDecodeOptionsIsStrict covers the authored shapes the adapter accepts and
// refuses, so a misspelled or missing key is reported instead of ignored.
func TestDecodeOptionsIsStrict(t *testing.T) {
	cases := []struct {
		name      string
		options   string
		want      string
		refused   bool
		label     string
		namespace string
	}{
		{
			name:      "valid",
			options:   "namespace: profile-a\nlabel: front",
			namespace: "profile-a", label: "front",
		},
		{
			name:    "missing namespace",
			options: "label: front",
			want:    "namespace is required",
			refused: true,
		},
		{
			name:    "unknown key",
			options: "namespace: profile-a\nnamespacee: typo",
			want:    "namespacee",
			refused: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bot := decodeBot(t, tc.options)
			value, reasons := testchannel.Definition().Decode(bot)
			if tc.refused {
				if len(reasons) == 0 {
					t.Fatalf("want a reason mentioning %q", tc.want)
				}
				if !strings.Contains(strings.Join(reasons, "; "), tc.want) {
					t.Fatalf("reasons = %v, want %q", reasons, tc.want)
				}
				return
			}
			if len(reasons) != 0 {
				t.Fatalf("reasons = %v", reasons)
			}
			options, ok := value.(testchannel.BotOptions)
			if !ok {
				t.Fatalf("decoded %T, want BotOptions", value)
			}
			if options.Namespace != tc.namespace || options.Label != tc.label {
				t.Fatalf("options = %+v, want namespace %q label %q",
					options, tc.namespace, tc.label)
			}
		})
	}
}

// TestOptionsSchemaAgreesWithTheDecoder proves the generated schema closes the
// object and requires the same key the decoder requires (ADR 0054).
func TestOptionsSchemaAgreesWithTheDecoder(t *testing.T) {
	schema := testchannel.Definition().OptionsSchema()
	if schema == nil {
		t.Fatal("the definition has no options schema")
	}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	if closed, ok := raw["additionalProperties"].(bool); !ok || closed {
		t.Fatalf("additionalProperties = %v, want false", raw["additionalProperties"])
	}
	required, _ := raw["required"].([]any)
	if len(required) != 1 || required[0] != "namespace" {
		t.Fatalf("required = %v, want [namespace]", required)
	}
}

// TestDeclarationIsHonest states what the adapter implements, so discovery and
// validation agree with it (ADR 0052).
func TestDeclarationIsHonest(t *testing.T) {
	def := testchannel.Definition()
	if def.Provider != testchannel.Provider || def.Modes[0] != testchannel.Mode {
		t.Fatalf("identity = %s/%s", def.Provider, def.Modes[0])
	}
	if !def.Capabilities.SupportsSend(botflota.KindText) ||
		!def.Capabilities.SupportsReceive(botflota.KindText) {
		t.Fatal("text must be declared in both directions")
	}
	if def.Capabilities.SupportsSend(botflota.KindImage) {
		t.Fatal("an image must not be declared")
	}
	if len(def.Capabilities.Features) != 0 {
		t.Fatalf("features = %v, want none implemented", def.Capabilities.Features)
	}
}

// TestMessageSpaceIsTheBindingNamespace ties the persisted space to the
// adapter's own account namespace (ADR 0026, ADR 0051).
func TestMessageSpaceIsTheBindingNamespace(t *testing.T) {
	def := testchannel.Definition()
	binding := &botflota.Binding{
		Provider: testchannel.Provider, Namespace: "profile-a",
		Identity: botflota.IdentityKey{Provider: testchannel.Provider, Subject: "acct"},
	}
	if got := def.MessageSpace(binding); got != "profile-a" {
		t.Fatalf("space = %q, want profile-a", got)
	}
	other := &botflota.Binding{Provider: "another"}
	if got := def.MessageSpace(other); got != "" {
		t.Fatalf("space of a foreign binding = %q, want empty", got)
	}
}

func decodeBot(t *testing.T, options string) spec.Bot {
	t.Helper()
	var bot struct {
		Channel spec.Channel `yaml:"channel"`
	}
	document := "channel:\n  provider: testchannel\n  mode: local\n  options:\n"
	for _, line := range strings.Split(options, "\n") {
		if line != "" {
			document += "    " + line + "\n"
		}
	}
	if err := yaml.Unmarshal([]byte(document), &bot); err != nil {
		t.Fatalf("unmarshal bot: %v", err)
	}
	return spec.Bot{Channel: bot.Channel}
}
