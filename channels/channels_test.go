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

func TestCatalogResolvesTheNamedImplementation(t *testing.T) {
	catalog := testCatalog(t, Definition{
		Provider: "whatsapp", Kind: "fake", Modes: []string{"native"},
	})
	def, key, err := catalog.Resolve(spec.Channel{Provider: "whatsapp", Mode: "native"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if key != (Key{Provider: "whatsapp", Mode: "native"}) {
		t.Fatalf("key = %+v", key)
	}
	if def.Kind != "fake" {
		t.Fatalf("definition = %+v", def)
	}
}

func TestCatalogRefusesAnUninstalledSelection(t *testing.T) {
	catalog := testCatalog(t, Definition{
		Provider: "fake", Kind: "fake", Modes: []string{"text"},
	})
	cases := []struct {
		name string
		sel  spec.Channel
		want string
	}{
		{"unknown provider", spec.Channel{Provider: "telegram", Mode: "bot"},
			"not installed"},
		{"unknown mode", spec.Channel{Provider: "fake", Mode: "voice"},
			"not installed"},
		{"missing provider", spec.Channel{Mode: "text"},
			"provider is required"},
		{"missing mode", spec.Channel{Provider: "fake"},
			"mode is required"},
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

// TestCatalogDoesNotShareMutableDefinitions proves the catalog is immutable: a
// slice or map a caller still holds cannot change what the catalog answers
// (ADR 0052).
func TestCatalogDoesNotShareMutableDefinitions(t *testing.T) {
	caps := Capabilities{
		Send:         []botflota.MessageKind{botflota.KindText},
		CardVariants: []spec.ContactEntryKind{spec.ContactPhone},
	}
	modes := []string{"native"}
	catalog := testCatalog(t, Definition{
		Provider: "fake", Kind: "fake", Modes: modes, Capabilities: caps,
	})

	caps.Send[0] = botflota.KindImage
	caps.CardVariants[0] = spec.ContactURL
	modes[0] = "changed"

	got, ok := catalog.Lookup("fake", "native")
	if !ok {
		t.Fatal("mutating the caller's slices changed the registered key")
	}
	if !got.Capabilities.SupportsSend(botflota.KindText) ||
		got.Capabilities.SupportsSend(botflota.KindImage) {
		t.Fatal("the catalog shares the caller's Send slice")
	}
	if !got.Capabilities.SupportsCardVariant(spec.ContactPhone) {
		t.Fatal("the catalog shares the caller's CardVariants slice")
	}

	got.Capabilities.Send[0] = botflota.KindVideo
	got.Capabilities.CardVariants[0] = spec.ContactURL
	got.Modes[0] = "changed"
	again, _ := catalog.Lookup("fake", "native")
	if !again.Capabilities.SupportsSend(botflota.KindText) {
		t.Fatal("Lookup leaked the internal Send slice")
	}
	if !again.Capabilities.SupportsCardVariant(spec.ContactPhone) {
		t.Fatal("Lookup leaked the internal CardVariants slice")
	}
	if again.Modes[0] != "native" {
		t.Fatal("Lookup leaked the internal Modes slice")
	}

	for _, def := range catalog.Definitions() {
		def.Capabilities.Send[0] = botflota.KindImage
		def.Modes[0] = "changed"
	}
	again, _ = catalog.Lookup("fake", "native")
	if !again.Capabilities.SupportsSend(botflota.KindText) || again.Modes[0] != "native" {
		t.Fatal("Definitions leaked the internal definition")
	}
}
