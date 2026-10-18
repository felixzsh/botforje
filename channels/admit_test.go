package channels

import (
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

func admitDefinition() Definition {
	return Definition{
		Provider: "whatsapp",
		Kind:     "fake",
		Modes:    []string{"native"},
		Capabilities: Capabilities{
			Send: []botflota.MessageKind{botflota.KindText, botflota.KindContact},
			CardVariants: []spec.ContactEntryKind{
				spec.ContactPhone,
			},
		},
		// Like the WhatsApp definition, the hook owns the provider match:
		// selector ownership stays with the caller, delivery refusal here.
		ValidateTarget: func(target botflota.ChatTarget) []string {
			if target.Provider != "whatsapp" {
				return []string{"target provider is not whatsapp"}
			}
			if target.Kind != botflota.ChatDirect {
				return []string{"shared conversations are not supported"}
			}
			return nil
		},
	}
}

func admitTarget() botflota.ChatTarget {
	return botflota.ChatTarget{
		Provider: "whatsapp", Kind: botflota.ChatDirect, Thread: "5215500000000",
	}
}

// TestAdmitSendRefusesBeforeQueueAdmission: declared content support and the
// definition's target rules decide before anything is persisted; every
// refusal names its reason (ADR 0044, ADR 0052).
func TestAdmitSendRefusesBeforeQueueAdmission(t *testing.T) {
	def := admitDefinition()
	target := admitTarget()
	tests := []struct {
		name    string
		target  botflota.ChatTarget
		content botflota.SendableContent
		wantOK  bool
	}{
		{
			name:    "supported text to a direct target",
			target:  target,
			content: botflota.TextContent{Body: "hola"},
			wantOK:  true,
		},
		{
			name:    "undeclared image kind",
			target:  target,
			content: botflota.MediaContent{Type: botflota.MediaImage},
			wantOK:  false,
		},
		{
			name:    "foreign provider target",
			target:  botflota.ChatTarget{Provider: "pager", Kind: botflota.ChatDirect, Thread: "x"},
			content: botflota.TextContent{Body: "hola"},
			wantOK:  false,
		},
		{
			name:    "shared conversation refused by the definition",
			target:  botflota.ChatTarget{Provider: "whatsapp", Kind: botflota.ChatGroup, Thread: "g"},
			content: botflota.TextContent{Body: "hola"},
			wantOK:  false,
		},
		{
			name:    "malformed target with no thread",
			target:  botflota.ChatTarget{Provider: "whatsapp", Kind: botflota.ChatDirect},
			content: botflota.TextContent{Body: "hola"},
			wantOK:  false,
		},
		{
			name:   "contact card with an unsupported entry kind",
			target: target,
			content: botflota.ContactContent{Name: "Ana", Entries: []botflota.ContactEntry{
				{Kind: "email", Value: "ana@example.test"},
			}},
			wantOK: false,
		},
		{
			name:   "contact card with a supported entry kind",
			target: target,
			content: botflota.ContactContent{Name: "Ana", Entries: []botflota.ContactEntry{
				{Kind: "phone", Value: "5215500000000"},
			}},
			wantOK: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reasons := AdmitSend(def, test.target, test.content)
			if test.wantOK && len(reasons) > 0 {
				t.Fatalf("AdmitSend refused a valid send: %q", reasons)
			}
			if !test.wantOK && len(reasons) == 0 {
				t.Fatal("AdmitSend accepted what the definition cannot deliver")
			}
		})
	}
}

// TestAdmitSendChecksCardVariantsThroughBothShapes proves the per-variant
// admission check runs for a contact payload whether the caller passes the
// value or a pointer to it (ADR 0052). A value receiver gives the pointer
// the same method set, so a value-only type assertion would let a pointer
// skip the variant checks.
func TestAdmitSendChecksCardVariantsThroughBothShapes(t *testing.T) {
	def := admitDefinition()
	target := admitTarget()
	unsupported := botflota.ContactContent{Name: "Ana",
		Entries: []botflota.ContactEntry{
			{Kind: "email", Value: "ana@example.test"},
		}}
	supported := botflota.ContactContent{Name: "Ana",
		Entries: []botflota.ContactEntry{
			{Kind: "phone", Value: "5215500000000"},
		}}
	tests := []struct {
		name    string
		content botflota.SendableContent
		wantOK  bool
	}{
		{"value with an unsupported entry kind", unsupported, false},
		{"pointer with an unsupported entry kind", &unsupported, false},
		{"value with a supported entry kind", supported, true},
		{"pointer with a supported entry kind", &supported, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reasons := AdmitSend(def, target, test.content)
			if test.wantOK && len(reasons) > 0 {
				t.Fatalf("AdmitSend refused a valid send: %q", reasons)
			}
			if !test.wantOK && len(reasons) == 0 {
				t.Fatal("AdmitSend accepted an unsupported card variant")
			}
		})
	}
}
