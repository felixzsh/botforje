package botflota

import (
	"reflect"
	"testing"
)

// TestContentRoundTrip proves every content type survives the storage
// encoding, structured fields included (ADR 0036).
func TestContentRoundTrip(t *testing.T) {
	cases := []MessageContent{
		TextContent{Body: "hi"},
		MediaContent{
			Type: MediaImage, URL: "https://x/a.png", Caption: "look",
			MIME: "image/png",
		},
		MediaContent{Type: MediaDocument, Path: "/tmp/a.pdf", Name: "a.pdf"},
		MediaContent{Type: MediaSticker, MIME: "image/webp"},
		LocationContent{Latitude: 19.43, Longitude: -99.13, Name: "Office"},
		ContactContent{Name: "Ana", Phone: "5215500000000"},
		PollContent{Question: "Lunch?", Options: []string{"yes", "no"}, Selectable: 1},
		EventContent{Name: "meeting"},
		InteractiveContent{Text: "Yes"},
		CommerceContent{Title: "Order 1"},
		CallContent{Label: "Missed call"},
		UnknownContent{},
	}
	for _, content := range cases {
		kind := content.MessageKind()
		t.Run(string(kind), func(t *testing.T) {
			data, err := ContentJSON(content)
			if err != nil {
				t.Fatalf("ContentJSON: %v", err)
			}
			got, err := ContentFromJSON(kind, content.VisibleText(), data)
			if err != nil {
				t.Fatalf("ContentFromJSON: %v", err)
			}
			if !reflect.DeepEqual(got, content) {
				t.Fatalf("round trip = %#v, want %#v", got, content)
			}
		})
	}
}

// TestContentFromText covers a row written before the structured column
// existed: only its kind and visible text are left (ADR 0036).
func TestContentFromText(t *testing.T) {
	cases := []struct {
		kind MessageKind
		text string
		want MessageContent
	}{
		{KindText, "hi", TextContent{Body: "hi"}},
		{KindImage, "look", MediaContent{Type: MediaImage, Caption: "look"}},
		{KindVideo, "", MediaContent{Type: MediaVideo}},
		{KindDocument, "a.pdf", MediaContent{Type: MediaDocument, Caption: "a.pdf"}},
		{KindLocation, "Office", LocationContent{Name: "Office"}},
		{KindContact, "Ana", ContactContent{Name: "Ana"}},
		{KindPoll, "Lunch?", PollContent{Question: "Lunch?"}},
		{KindEvent, "meeting", EventContent{Name: "meeting"}},
		{KindInteractive, "Yes", InteractiveContent{Text: "Yes"}},
		{KindCommerce, "Order", CommerceContent{Title: "Order"}},
		{KindCall, "Missed call", CallContent{Label: "Missed call"}},
		{KindUnknown, "", UnknownContent{}},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			got, err := ContentFromJSON(tc.kind, tc.text, []byte("{}"))
			if err != nil {
				t.Fatalf("ContentFromJSON: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("fallback = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestContentErrors(t *testing.T) {
	if _, err := ContentJSON(nil); err == nil {
		t.Fatal("nil content must not encode")
	}
	if _, err := ContentFromJSON("nope", "", []byte(`{"x":1}`)); err == nil {
		t.Fatal("an unknown kind must not decode")
	}
	if _, err := ContentFromJSON(KindText, "", []byte(`not json`)); err == nil {
		t.Fatal("malformed content must not decode")
	}
}

// TestContentHelpersAreNilSafe covers the read helpers a logger or a handler
// uses before the engine normalizes the payload.
func TestContentHelpersAreNilSafe(t *testing.T) {
	if kind := ContentKind(nil); kind != "" {
		t.Fatalf("ContentKind(nil) = %q, want empty", kind)
	}
	if text := ContentText(nil); text != "" {
		t.Fatalf("ContentText(nil) = %q, want empty", text)
	}
}

// TestMediaTypeValid rejects a non-media type, so a MediaContent cannot claim a
// kind the channel would have to guess at.
func TestMediaTypeValid(t *testing.T) {
	for _, valid := range []MediaType{
		MediaImage, MediaVideo, MediaAudio, MediaDocument, MediaSticker,
	} {
		if !valid.Valid() {
			t.Fatalf("%q must be valid", valid)
		}
	}
	for _, invalid := range []MediaType{"", "text", "video_note"} {
		if invalid.Valid() {
			t.Fatalf("%q must be invalid", invalid)
		}
	}
}
