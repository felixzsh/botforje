package conformance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// allKinds is the ordered set of kinds the suite knows how to construct. A
// definition that declares none of the missing ones simply has no unsupported
// case to prove.
var allKinds = []botflota.MessageKind{
	botflota.KindText,
	botflota.KindImage,
	botflota.KindVideo,
	botflota.KindAudio,
	botflota.KindDocument,
	botflota.KindSticker,
	botflota.KindLocation,
	botflota.KindContact,
	botflota.KindPoll,
}

// cardVariants is every contact-entry kind a definition may declare, in a fixed
// order so the acceptance boundary is asserted for each of them.
var cardVariants = []spec.ContactEntryKind{
	spec.ContactPhone, spec.ContactEmail, spec.ContactAddress, spec.ContactURL,
}

// supportedKind is the first declared sendable kind the suite can construct.
func supportedKind(t *testing.T, def channels.Definition) botflota.MessageKind {
	t.Helper()
	for _, kind := range def.Capabilities.Send {
		if contentFor(def, kind) != nil {
			return kind
		}
	}
	t.Fatal("the definition declares no constructible sendable kind")
	return ""
}

// unsupportedKind is a constructible kind the definition does not declare, or
// empty when every constructible kind is supported.
func unsupportedKind(def channels.Definition) botflota.MessageKind {
	for _, kind := range allKinds {
		if contentFor(def, kind) != nil && !def.Capabilities.SupportsSend(kind) {
			return kind
		}
	}
	return ""
}

// supportedContent and unsupportedContent are the concrete payloads the suite
// submits to admission. They are non-nil because supportedKind and
// unsupportedKind only return constructible kinds.
func supportedContent(t *testing.T, def channels.Definition) botflota.SendableContent {
	t.Helper()
	return contentFor(def, supportedKind(t, def))
}

func unsupportedContent(
	t *testing.T, def channels.Definition,
) botflota.SendableContent {
	t.Helper()
	kind := unsupportedKind(def)
	if kind == "" {
		return nil
	}
	return contentFor(def, kind)
}

// contentFor builds one sendable payload of the given kind, or nil when the
// suite has no fixture for it. A contact card carries an entry for every
// variant the definition declares, so the shared per-variant admission runs
// instead of being skipped on an entry-less card.
func contentFor(def channels.Definition, kind botflota.MessageKind) botflota.SendableContent {
	switch kind {
	case botflota.KindText:
		return botflota.TextContent{Body: "hello"}
	case botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
		botflota.KindDocument, botflota.KindSticker:
		return botflota.MediaContent{
			Type: botflota.MediaType(kind),
			URL:  "urn:conformance:" + string(kind),
		}
	case botflota.KindLocation:
		return botflota.LocationContent{Latitude: 1.5, Longitude: 2.5}
	case botflota.KindContact:
		return cardContent(def.Capabilities.CardVariants)
	case botflota.KindPoll:
		return botflota.PollContent{Question: "Ready?", Options: []string{"yes", "no"}}
	}
	return nil
}

// cardContent builds a contact card with one entry per given variant, so the
// shared admission sees the variants itself rather than an empty entry list.
func cardContent(variants []spec.ContactEntryKind) botflota.ContactContent {
	card := botflota.ContactContent{Name: "Conformance"}
	for _, variant := range variants {
		card.Entries = append(card.Entries, botflota.ContactEntry{
			Kind: string(variant), Value: "conformance-" + string(variant),
		})
	}
	return card
}

// contactEntries is cardContent's authored counterpart, so config validation
// checks the same variants the runtime admission does.
func contactEntries(variants []spec.ContactEntryKind) []spec.ContactEntry {
	entries := make([]spec.ContactEntry, 0, len(variants))
	for _, variant := range variants {
		entries = append(entries, spec.ContactEntry{
			Kind: string(variant), Value: "conformance-" + string(variant),
		})
	}
	return entries
}

// checkCardVariants proves the shared admission enforces the declared card
// variants: an entry of a declared variant is accepted, an entry of any other
// valid variant is refused by name (ADR 0036, ADR 0052).
func checkCardVariants(t *testing.T, s Subject) {
	t.Helper()
	def := s.declaration()
	if !def.Capabilities.SupportsSend(botflota.KindContact) {
		t.Skip("the definition declares no contact card")
	}
	target := supportedTarget(s)
	for _, variant := range cardVariants {
		t.Run(string(variant), func(t *testing.T) {
			reasons := channels.AdmitSend(
				def, target, cardContent([]spec.ContactEntryKind{variant}))
			if def.Capabilities.SupportsCardVariant(variant) {
				if len(reasons) > 0 {
					t.Fatalf("admission refused the declared variant %s: %v",
						variant, reasons)
				}
				return
			}
			if len(reasons) == 0 {
				t.Fatalf("admission accepted the undeclared variant %s", variant)
			}
			if !strings.Contains(strings.Join(reasons, "; "), string(variant)) {
				t.Fatalf("refusal %v does not name the undeclared variant %s",
					reasons, variant)
			}
		})
	}
}

// stepFor builds the authored step that sends one kind, so config validation
// is exercised the way an author writes it. Its card step carries the declared
// variants, matching the runtime content path.
func stepFor(def channels.Definition, kind botflota.MessageKind) spec.Step {
	switch kind {
	case botflota.KindText:
		return spec.Step{Message: &spec.Message{Body: spec.Text{"hello"}}}
	case botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
		botflota.KindDocument, botflota.KindSticker:
		return spec.Step{Media: &spec.Media{
			Kind: string(kind), URL: "urn:conformance:" + string(kind),
		}}
	case botflota.KindLocation:
		return spec.Step{Location: &spec.Location{Latitude: 1.5, Longitude: 2.5}}
	case botflota.KindContact:
		return spec.Step{Contact: &spec.Contact{
			Name:    "Conformance",
			Entries: contactEntries(def.Capabilities.CardVariants),
		}}
	case botflota.KindPoll:
		return spec.Step{Poll: &spec.Poll{
			Question: "Ready?", Options: []string{"yes", "no"},
		}}
	}
	return spec.Step{}
}

func targetJSON(target botflota.ChatTarget) string {
	data, err := json.Marshal(map[string]string{
		"provider": target.Provider,
		"kind":     string(target.Kind),
		"thread":   target.Thread,
		"topic":    target.Topic,
	})
	if err != nil {
		return "{}"
	}
	return string(data)
}
