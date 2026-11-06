package channel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow"

	"github.com/felixzsh/botflota"
)

func TestBuildContent(t *testing.T) {
	text, err := buildContent(botflota.TextContent{Body: "hi"})
	if err != nil {
		t.Fatalf("text: %v", err)
	}
	if text.GetConversation() != "hi" {
		t.Fatalf("text = %q", text.GetConversation())
	}

	location, err := buildContent(botflota.LocationContent{
		Latitude: 1.5, Longitude: -2.5, Name: "Office",
	})
	if err != nil {
		t.Fatalf("location: %v", err)
	}
	got := location.GetLocationMessage()
	if got.GetDegreesLatitude() != 1.5 || got.GetDegreesLongitude() != -2.5 {
		t.Fatalf("coordinates = %v, %v", got.GetDegreesLatitude(), got.GetDegreesLongitude())
	}
	if got.GetName() != "Office" {
		t.Fatalf("name = %q", got.GetName())
	}

	contact, err := buildContent(botflota.ContactContent{
		Name: "Ana",
		Entries: []botflota.ContactEntry{
			{Kind: "phone", Value: "+5215500000000", Label: "work"},
			{Kind: "email", Value: "ana@example.com"},
		},
		Note: "Friend",
	})
	if err != nil {
		t.Fatalf("contact: %v", err)
	}
	card := contact.GetContactMessage()
	vc := card.GetVcard()
	if card.GetDisplayName() != "Ana" ||
		!strings.Contains(vc, "TEL;type=CELL;type=VOICE;type=WORK;waid=5215500000000:+5215500000000") ||
		!strings.Contains(vc, "EMAIL:ana@example.com") ||
		!strings.Contains(vc, "NOTE:Friend") {
		t.Fatalf("contact = %+v", card)
	}

	poll, err := buildContent(botflota.PollContent{
		Question: "Lunch?", Options: []string{"yes", "no"},
	})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	created := poll.GetPollCreationMessage()
	if created.GetName() != "Lunch?" || len(created.GetOptions()) != 2 ||
		created.GetSelectableOptionsCount() != 1 {
		t.Fatalf("poll = %+v", created)
	}
	if poll.GetMessageContextInfo().GetMessageSecret() == nil {
		t.Fatal("a poll needs its message secret for vote decryption")
	}

	if _, err := buildContent(nil); err == nil {
		t.Fatal("nil content must fail")
	}
	if _, err := buildContent(botflota.PollContent{
		Question: "Lunch?", Options: []string{"only"},
	}); err == nil {
		t.Fatal("a poll with one option must fail")
	}
}

func TestContentForMedia(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploads := 0
	a := &Adapter{
		upload: func(
			_ context.Context, data []byte, mediaType whatsmeow.MediaType,
		) (whatsmeow.UploadResponse, error) {
			uploads++
			if mediaType != whatsmeow.MediaImage {
				t.Fatalf("media type = %q, want image", mediaType)
			}
			if string(data) != "png-bytes" {
				t.Fatalf("uploaded %q", data)
			}
			return whatsmeow.UploadResponse{
				URL: "https://media", DirectPath: "/direct", FileLength: 9,
			}, nil
		},
	}
	content, err := a.contentFor(context.Background(), botflota.OutgoingMessage{
		Content: botflota.MediaContent{
			Type: botflota.MediaImage, Path: path, Caption: "look",
		},
	})
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	image := content.GetImageMessage()
	if image == nil || image.GetCaption() != "look" || image.GetURL() != "https://media" {
		t.Fatalf("image = %+v", image)
	}
	if uploads != 1 {
		t.Fatalf("uploads = %d, want 1", uploads)
	}

	// A media type the channel cannot build is rejected, not silently dropped.
	if _, err := a.contentFor(context.Background(), botflota.OutgoingMessage{
		Content: botflota.MediaContent{Type: "video_note", Path: path},
	}); !errors.Is(err, botflota.ErrUnsupportedKind) {
		t.Fatalf("unknown media type = %v, want unsupported", err)
	}
}

// TestMediaPathIsReadInsideTheFileRoot: the adapter enforces the scope's root
// where the bytes are actually opened, so no step can read past it and no
// refusal falls back to an unconfined read (ADR 0047).
func TestMediaPathIsReadInsideTheFileRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("photo.png", "png-bytes")
	outside := filepath.Join(base, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside.txt", filepath.Join(root, "escape")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unreadable"), []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		root      string
		path      string
		want      string
		outside   bool
		permanent bool
	}{
		{name: "relative path resolves under the root", root: root,
			path: "photo.png", want: "png-bytes"},
		{name: "absolute path is refused", root: root, path: outside,
			outside: true, permanent: true},
		{name: "a path above the root is refused", root: root,
			path: "../outside.txt", outside: true, permanent: true},
		{name: "a symbolic link out of the root is refused", root: root,
			path: "escape", outside: true, permanent: true},
		{name: "an unreadable file fails the send", root: root,
			path: "unreadable"},
		{name: "a missing file fails the send", root: root, path: "missing.png"},
		{name: "no root keeps today's behavior", path: outside, want: "outside"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path == "unreadable" && os.Geteuid() == 0 {
				t.Skip("the process can read any file as root")
			}
			a := &Adapter{fileRoot: tc.root}
			data, err := a.readMedia(context.Background(), botflota.MediaContent{
				Type: botflota.MediaImage, Path: tc.path,
			})
			if err != nil {
				failure, ok := botflota.AsSendFailure(err)
				if !ok {
					t.Fatalf("error %v is not a send failure", err)
				}
				if tc.outside {
					if !errors.Is(err, botflota.ErrOutsideFileRoot) {
						t.Fatalf("error %v is not a file-root refusal", err)
					}
					if failure.Permanent != tc.permanent {
						t.Fatalf("refusal permanent = %v, want %v",
							failure.Permanent, tc.permanent)
					}
					return
				}
				if errors.Is(err, botflota.ErrOutsideFileRoot) {
					t.Fatalf("error %v reports an escape it is not", err)
				}
				return
			}
			if tc.outside || tc.want == "" {
				t.Fatalf("read %q = %q, want a refusal", tc.path, data)
			}
			if string(data) != tc.want {
				t.Fatalf("read %q = %q, want %q", tc.path, data, tc.want)
			}
		})
	}
}
