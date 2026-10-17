package conformance

import (
	"encoding/json"
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

// supportedKind is the first declared sendable kind the suite can construct.
func supportedKind(t *testing.T, def channels.Definition) botflota.MessageKind {
	t.Helper()
	for _, kind := range def.Capabilities.Send {
		if contentFor(kind) != nil {
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
		if contentFor(kind) != nil && !def.Capabilities.SupportsSend(kind) {
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
	return contentFor(supportedKind(t, def))
}

func unsupportedContent(
	t *testing.T, def channels.Definition,
) botflota.SendableContent {
	t.Helper()
	kind := unsupportedKind(def)
	if kind == "" {
		return nil
	}
	return contentFor(kind)
}

// contentFor builds one sendable payload of the given kind, or nil when the
// suite has no fixture for it.
func contentFor(kind botflota.MessageKind) botflota.SendableContent {
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
		return botflota.ContactContent{Name: "Conformance"}
	case botflota.KindPoll:
		return botflota.PollContent{Question: "Ready?", Options: []string{"yes", "no"}}
	}
	return nil
}

// stepFor builds the authored step that sends one kind, so config validation
// is exercised the way an author writes it.
func stepFor(kind botflota.MessageKind) spec.Step {
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
		return spec.Step{Contact: &spec.Contact{Name: "Conformance"}}
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
