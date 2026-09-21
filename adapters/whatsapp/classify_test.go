package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota"
)

func TestContentType(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		kind botflota.MessageKind
		body string
		ok   bool
	}{
		{"text", &waE2E.Message{Conversation: proto.String("hi")},
			botflota.KindText, "hi", true},
		{"extended text", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String("link")}}, botflota.KindText, "link", true},
		{"image caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String("look")}}, botflota.KindImage, "look", true},
		{"image without caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}},
			botflota.KindImage, "", true},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{}},
			botflota.KindVideo, "", true},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{}},
			botflota.KindAudio, "", true},
		{"document file name", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			FileName: proto.String("a.pdf")}}, botflota.KindDocument, "a.pdf", true},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}},
			botflota.KindSticker, "", true},
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			Name: proto.String("Office")}}, botflota.KindLocation, "Office", true},
		{"live location", &waE2E.Message{LiveLocationMessage: &waE2E.LiveLocationMessage{}},
			botflota.KindLocation, "", true},
		{"contact", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{
			DisplayName: proto.String("Ana")}}, botflota.KindContact, "Ana", true},
		{"poll", &waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{
			Name: proto.String("Lunch?")}}, botflota.KindPoll, "Lunch?", true},
		{"event", &waE2E.Message{EventMessage: &waE2E.EventMessage{
			Name: proto.String("meeting")}}, botflota.KindEvent, "meeting", true},
		{"missed call", &waE2E.Message{CallLogMesssage: &waE2E.CallLogMessage{
			CallOutcome: waE2E.CallLogMessage_MISSED.Enum()}},
			botflota.KindCall, "Missed call", true},
		{"interactive list reply", &waE2E.Message{
			ListResponseMessage: &waE2E.ListResponseMessage{
				Title: proto.String("Yes"),
			}}, botflota.KindInteractive, "Yes", true},
		{"commerce order", &waE2E.Message{OrderMessage: &waE2E.OrderMessage{
			OrderTitle: proto.String("Order 1")}}, botflota.KindCommerce, "Order 1", true},
		{"reaction is control", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{}},
			"", "", false},
		{"protocol is control", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}},
			"", "", false},
		{"edit is control", &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{}},
			"", "", false},
		{"unknown content", &waE2E.Message{PlaceholderMessage: &waE2E.PlaceholderMessage{}},
			botflota.KindUnknown, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, ok := contentType(tc.msg)
			if ok != tc.ok {
				t.Fatalf("contentType ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if content.MessageKind() != tc.kind || content.VisibleText() != tc.body {
				t.Fatalf("contentType = (%q, %q), want (%q, %q)",
					content.MessageKind(), content.VisibleText(), tc.kind, tc.body)
			}
		})
	}
}

func TestContentTypeExtractsStructure(t *testing.T) {
	poll, ok := contentType(&waE2E.Message{PollCreationMessage: &waE2E.PollCreationMessage{
		Name: proto.String("Lunch?"),
		Options: []*waE2E.PollCreationMessage_Option{
			{OptionName: proto.String("yes")},
			{OptionName: proto.String("no")},
		},
		SelectableOptionsCount: proto.Uint32(1),
	}})
	if !ok {
		t.Fatal("a poll was not classified")
	}
	question, ok := poll.(botflota.PollContent)
	if !ok || len(question.Options) != 2 || question.Selectable != 1 {
		t.Fatalf("poll = %#v", poll)
	}

	location, ok := contentType(&waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		DegreesLatitude:  proto.Float64(19.43),
		DegreesLongitude: proto.Float64(-99.13),
	}})
	if !ok {
		t.Fatal("a location was not classified")
	}
	place, ok := location.(botflota.LocationContent)
	if !ok || place.Latitude != 19.43 || place.Longitude != -99.13 {
		t.Fatalf("location = %#v", location)
	}
}

func TestUnwrapMessage(t *testing.T) {
	wrapped := &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{
		Message: &waE2E.Message{Conversation: proto.String("secret")},
	}}
	if inner := unwrapMessage(wrapped); inner.GetConversation() != "secret" {
		t.Fatalf("unwrap = %+v", inner)
	}
	if unwrapMessage(&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}}) != nil {
		t.Fatal("a protocol message must not unwrap to content")
	}
}
