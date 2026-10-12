package channels

import (
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

func testCatalog(t *testing.T, defs ...Definition) *Catalog {
	t.Helper()
	catalog, err := New(defs...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return catalog
}

func TestCatalogResolvesTheDocumentedDefault(t *testing.T) {
	catalog := testCatalog(t, Definition{
		Provider: "whatsapp", Kind: "fake", Modes: []string{"native"},
	})
	def, key, err := catalog.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if key != (Key{Provider: DefaultProvider, Mode: DefaultMode}) {
		t.Fatalf("key = %+v, want the default %s/%s", key, DefaultProvider, DefaultMode)
	}
	if def.Provider != "whatsapp" || def.Kind != "fake" {
		t.Fatalf("definition = %+v", def)
	}
}

func TestCatalogRefusesAnUninstalledSelection(t *testing.T) {
	catalog := testCatalog(t, Definition{
		Provider: "fake", Kind: "fake", Modes: []string{"text"},
	})
	cases := []struct {
		name string
		sel  *spec.Channel
		want string
	}{
		{"unknown provider", &spec.Channel{Provider: "telegram", Mode: "bot"},
			"not installed"},
		{"unknown mode", &spec.Channel{Provider: "fake", Mode: "voice"},
			"not installed"},
		{"missing provider", &spec.Channel{Mode: "text"},
			"provider is required"},
		{"missing mode", &spec.Channel{Provider: "fake"},
			"mode is required"},
		{"default not installed", nil, "no channel is installed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := catalog.Resolve(tc.sel); err == nil {
				t.Fatalf("Resolve(%+v) = nil error, want %q", tc.sel, tc.want)
			}
		})
	}
}

func TestNewRejectsAmbiguousOrIncompleteDefinitions(t *testing.T) {
	cases := []struct {
		name string
		defs []Definition
	}{
		{"no provider", []Definition{{Modes: []string{"native"}}}},
		{"no mode", []Definition{{Provider: "fake"}}},
		{"duplicate provider and mode", []Definition{
			{Provider: "fake", Modes: []string{"native"}},
			{Provider: "fake", Modes: []string{"native"}},
		}},
		{"overlapping mode", []Definition{
			{Provider: "fake", Modes: []string{"native"}},
			{Provider: "fake", Modes: []string{"native", "text"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.defs...); err == nil {
				t.Fatal("New accepted an ambiguous catalog")
			}
		})
	}
}

func TestCapabilitiesReportDeclaredSupport(t *testing.T) {
	caps := Capabilities{
		Send:         []botflota.MessageKind{botflota.KindText},
		Receive:      []botflota.MessageKind{botflota.KindText, botflota.KindImage},
		Features:     []string{FeatureHistoryImport},
		CardVariants: []spec.ContactEntryKind{spec.ContactPhone},
		Limits:       map[string]int{"text_characters": 4096},
	}
	if !caps.SupportsSend(botflota.KindText) {
		t.Error("text should be sendable")
	}
	if caps.SupportsSend(botflota.KindImage) {
		t.Error("image must not claim send support")
	}
	if !caps.SupportsReceive(botflota.KindImage) {
		t.Error("image should be receivable")
	}
	if !caps.HasFeature(FeatureHistoryImport) || caps.HasFeature(FeaturePresence) {
		t.Error("feature lookup is wrong")
	}
	if !caps.SupportsCardVariant(spec.ContactPhone) ||
		caps.SupportsCardVariant(spec.ContactURL) {
		t.Error("card variant lookup is wrong")
	}
	if limit, ok := caps.Limit("text_characters"); !ok || limit != 4096 {
		t.Errorf("limit = %d, %v", limit, ok)
	}
	if _, ok := caps.Limit("unknown"); ok {
		t.Error("an undeclared limit must report false")
	}
}

func TestDefinitionsListsEachProviderAndModeOnce(t *testing.T) {
	catalog := testCatalog(t,
		Definition{Provider: "whatsapp", Kind: "fake", Modes: []string{"native"}},
		Definition{Provider: "fake", Kind: "fake", Modes: []string{"text"}},
	)
	defs := catalog.Definitions()
	if len(defs) != 2 {
		t.Fatalf("definitions = %d, want 2", len(defs))
	}
	if defs[0].Provider != "fake" || defs[1].Provider != "whatsapp" {
		t.Fatalf("definitions are not ordered: %+v", defs)
	}
}
