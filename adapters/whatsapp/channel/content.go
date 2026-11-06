package channel

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/egress"
)

// maxMediaSize bounds what the adapter buffers before upload, so a large file
// or a slow URL cannot exhaust the process (ADR 0036).
const maxMediaSize = 16 << 20

// uploader is a field so a test injects a fake instead of a live connection.
type uploader func(
	ctx context.Context, data []byte, mediaType whatsmeow.MediaType,
) (whatsmeow.UploadResponse, error)

// contentFor needs the live connection only for media, which uploads its
// bytes first; every other kind is built locally (ADR 0036).
func (a *Adapter) contentFor(
	ctx context.Context, msg botflota.OutgoingMessage,
) (*waE2E.Message, error) {
	if content, ok := msg.Content.(botflota.MediaContent); ok {
		return a.buildMedia(ctx, content)
	}
	return buildContent(msg.Content)
}

func buildContent(content botflota.SendableContent) (*waE2E.Message, error) {
	switch c := content.(type) {
	case nil:
		return nil, errors.New("whatsapp: message has no content")
	case botflota.TextContent:
		return &waE2E.Message{Conversation: proto.String(c.Body)}, nil
	case botflota.LocationContent:
		return buildLocation(c), nil
	case botflota.ContactContent:
		return buildContact(c), nil
	case botflota.PollContent:
		return buildPoll(c)
	default:
		return nil, fmt.Errorf("%w: %T", botflota.ErrUnsupportedKind, content)
	}
}

func buildLocation(c botflota.LocationContent) *waE2E.Message {
	return &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		DegreesLatitude:  proto.Float64(c.Latitude),
		DegreesLongitude: proto.Float64(c.Longitude),
		Name:             optional(c.Name),
		Address:          optional(c.Address),
		URL:              optional(c.URL),
		Comment:          optional(c.Description),
	}}
}

func buildContact(c botflota.ContactContent) *waE2E.Message {
	return &waE2E.Message{ContactMessage: &waE2E.ContactMessage{
		DisplayName: proto.String(c.Name),
		Vcard:       proto.String(vcard(c)),
	}}
}

// vcard renders entries as the vCard 3.0 text WhatsApp expects. Values
// stay opaque: the adapter escapes the field separators but never
// interprets the content. A phone entry keeps the waid parameter WhatsApp
// uses for account matching when the value holds digits. Unknown entry
// kinds ride along as NOTE lines so nothing is dropped silently.
func vcard(contact botflota.ContactContent) string {
	lines := []string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"FN:" + vcardEscape(contact.Name),
		"N:" + vcardEscape(contact.Name) + ";;;;",
	}
	for _, e := range contact.Entries {
		switch e.Kind {
		case "phone":
			line := "TEL;type=CELL;type=VOICE" + vcardType(e.Label)
			if digits := vcardDigits(e.Value); digits != "" {
				line += ";waid=" + digits
			}
			lines = append(lines, line+":"+vcardEscape(e.Value))
		case "email":
			lines = append(lines, "EMAIL"+vcardType(e.Label)+":"+vcardEscape(e.Value))
		case "address":
			lines = append(lines, "ADR"+vcardType(e.Label)+":;;"+vcardEscape(e.Value)+";;;;")
		case "url":
			lines = append(lines, "URL"+vcardType(e.Label)+":"+vcardEscape(e.Value))
		default:
			lines = append(lines, "NOTE:"+vcardEscape(e.Kind+": "+e.Value))
		}
	}
	if contact.Note != "" {
		lines = append(lines, "NOTE:"+vcardEscape(contact.Note))
	}
	return strings.Join(lines, "\r\n") + "\r\nEND:VCARD"
}

// vcardType renders an entry label as a vCard TYPE parameter, or nothing
// when the author set no label.
func vcardType(label string) string {
	if label == "" {
		return ""
	}
	return ";type=" + strings.ToUpper(label)
}

// vcardDigits keeps the ASCII digits of a phone entry for the waid
// parameter, or nothing when the value holds no digits.
func vcardDigits(value string) string {
	var digits strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] >= '0' && value[i] <= '9' {
			digits.WriteByte(value[i])
		}
	}
	return digits.String()
}

// vcardEscape escapes the vCard 3.0 field separators in one value.
func vcardEscape(value string) string {
	escaper := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`,
		"\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return escaper.Replace(value)
}

func buildPoll(content botflota.PollContent) (*waE2E.Message, error) {
	if len(content.Options) < 2 {
		return nil, errors.New("whatsapp: poll needs at least two options")
	}
	// The poll secret lets a voter's encrypted vote be read back; it travels
	// in the message context, exactly as whatsmeow's own builder does.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("whatsapp: poll secret: %w", err)
	}
	options := make([]*waE2E.PollCreationMessage_Option, len(content.Options))
	for i, name := range content.Options {
		options[i] = &waE2E.PollCreationMessage_Option{OptionName: proto.String(name)}
	}
	return &waE2E.Message{
		PollCreationMessage: &waE2E.PollCreationMessage{
			Name:                   proto.String(content.Question),
			Options:                options,
			SelectableOptionsCount: proto.Uint32(selectableCount(content)),
		},
		MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret},
	}, nil
}

// selectableCount picks one option by default, never more than the poll
// offers.
func selectableCount(content botflota.PollContent) uint32 {
	count := content.Selectable
	if count <= 0 {
		count = 1
	}
	if count > len(content.Options) {
		count = len(content.Options)
	}
	return uint32(count)
}

func (a *Adapter) buildMedia(
	ctx context.Context, content botflota.MediaContent,
) (*waE2E.Message, error) {
	if !content.Type.Valid() {
		return nil, fmt.Errorf("%w: media type %q", botflota.ErrUnsupportedKind, content.Type)
	}
	data, err := a.readMedia(ctx, content)
	if err != nil {
		return nil, err
	}
	mime := content.MIME
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	upload := a.upload
	if upload == nil {
		client, err := a.clientForUpload()
		if err != nil {
			return nil, err
		}
		upload = client.Upload
	}
	resp, err := upload(ctx, data, mediaTypeFor(content.Type))
	if err != nil {
		// The bytes never reached the wire: worth retrying, never a
		// proven rejection.
		return nil, botflota.TransientSendFailure(
			fmt.Errorf("whatsapp: upload media: %w", err), 0)
	}
	return mediaMessage(content, mime, resp)
}

func mediaMessage(
	content botflota.MediaContent, mime string, resp whatsmeow.UploadResponse,
) (*waE2E.Message, error) {
	switch content.Type {
	case botflota.MediaImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			Caption: optional(content.Caption), FileLength: proto.Uint64(resp.FileLength),
			MediaKey: resp.MediaKey, FileSHA256: resp.FileSHA256,
			FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	case botflota.MediaVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			Caption: optional(content.Caption), FileLength: proto.Uint64(resp.FileLength),
			MediaKey: resp.MediaKey, FileSHA256: resp.FileSHA256,
			FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	case botflota.MediaAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			FileLength: proto.Uint64(resp.FileLength), MediaKey: resp.MediaKey,
			FileSHA256: resp.FileSHA256, FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	case botflota.MediaDocument:
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			FileName: optional(content.Name), Title: optional(content.Name),
			Caption: optional(content.Caption), FileLength: proto.Uint64(resp.FileLength),
			MediaKey: resp.MediaKey, FileSHA256: resp.FileSHA256,
			FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	case botflota.MediaSticker:
		return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			FileLength: proto.Uint64(resp.FileLength), MediaKey: resp.MediaKey,
			FileSHA256: resp.FileSHA256, FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	default:
		return nil, fmt.Errorf("%w: media type %q", botflota.ErrUnsupportedKind, content.Type)
	}
}

// mediaTypeFor shares the image upload keys between images and stickers.
func mediaTypeFor(t botflota.MediaType) whatsmeow.MediaType {
	switch t {
	case botflota.MediaVideo:
		return whatsmeow.MediaVideo
	case botflota.MediaAudio:
		return whatsmeow.MediaAudio
	case botflota.MediaDocument:
		return whatsmeow.MediaDocument
	default:
		return whatsmeow.MediaImage
	}
}

func (a *Adapter) readMedia(
	ctx context.Context, content botflota.MediaContent,
) ([]byte, error) {
	switch {
	case content.URL != "":
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, content.URL, nil)
		if err != nil {
			return nil, fmt.Errorf("whatsapp: media url: %w", err)
		}
		client := a.http
		if client == nil {
			client = http.DefaultClient
		}
		response, err := client.Do(request)
		if err != nil {
			// A policy refusal is a decision that retrying cannot change, so it
			// fails now; a network failure stays worth another attempt
			// (ADR 0048).
			if errors.Is(err, egress.ErrBlocked) {
				return nil, botflota.PermanentSendFailure(
					fmt.Errorf("whatsapp: fetch media: %w", err))
			}
			return nil, botflota.TransientSendFailure(
				fmt.Errorf("whatsapp: fetch media: %w", err), 0)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, botflota.TransientSendFailure(fmt.Errorf(
				"whatsapp: fetch media: %s", response.Status), 0)
		}
		return readLimited(response.Body)
	case content.Path != "":
		file, err := os.Open(content.Path)
		if err != nil {
			return nil, botflota.TransientSendFailure(
				fmt.Errorf("whatsapp: open media: %w", err), 0)
		}
		defer file.Close()
		return readLimited(file)
	default:
		return nil, botflota.PermanentSendFailure(
			errors.New("whatsapp: media has neither url nor path"))
	}
}

func readLimited(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxMediaSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMediaSize {
		return nil, botflota.PermanentSendFailure(fmt.Errorf(
			"whatsapp: media exceeds %d bytes", maxMediaSize))
	}
	return data, nil
}

func (a *Adapter) clientForUpload() (*whatsmeow.Client, error) {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return nil, errors.New("whatsapp: not connected")
	}
	return client, nil
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return proto.String(value)
}
