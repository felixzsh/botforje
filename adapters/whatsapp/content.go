package whatsapp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota"
)

// maxMediaSize bounds an attachment the adapter reads into memory before it
// uploads it, so a large file or a slow URL cannot exhaust the process. The
// engine never fetches media (ADR 0036).
const maxMediaSize = 16 << 20

// uploader uploads attachment bytes. It is a field so a test injects a fake
// instead of a live connection.
type uploader func(
	ctx context.Context, data []byte, mediaType whatsmeow.MediaType,
) (whatsmeow.UploadResponse, error)

// contentFor maps an outbound message to the WhatsApp wire message. Media
// kinds upload their bytes first, so they need the live connection; every
// other kind is built locally (ADR 0036).
func (a *Adapter) contentFor(
	ctx context.Context, msg botflota.OutgoingMessage,
) (*waE2E.Message, error) {
	switch msg.Kind {
	case botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
		botflota.KindDocument, botflota.KindSticker:
		return a.buildMedia(ctx, msg)
	default:
		return buildContent(msg)
	}
}

// buildContent maps a message that needs neither network nor device: text,
// location, contact, and poll.
func buildContent(msg botflota.OutgoingMessage) (*waE2E.Message, error) {
	switch msg.Kind {
	case botflota.KindText, "":
		return &waE2E.Message{Conversation: proto.String(msg.Body)}, nil
	case botflota.KindLocation:
		return buildLocation(msg)
	case botflota.KindContact:
		return buildContact(msg)
	case botflota.KindPoll:
		return buildPoll(msg)
	case botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
		botflota.KindDocument, botflota.KindSticker:
		return nil, errors.New("whatsapp: media needs the connection to upload")
	default:
		return nil, fmt.Errorf("%w: %s", botflota.ErrUnsupportedKind, msg.Kind)
	}
}

func buildLocation(msg botflota.OutgoingMessage) (*waE2E.Message, error) {
	if msg.Location == nil {
		return nil, errors.New("whatsapp: location message without a location")
	}
	return &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		DegreesLatitude:  proto.Float64(msg.Location.Latitude),
		DegreesLongitude: proto.Float64(msg.Location.Longitude),
		Name:             optional(msg.Location.Name),
		Address:          optional(msg.Location.Address),
		URL:              optional(msg.Location.URL),
		Comment:          optional(msg.Location.Description),
	}}, nil
}

func buildContact(msg botflota.OutgoingMessage) (*waE2E.Message, error) {
	if msg.Contact == nil {
		return nil, errors.New("whatsapp: contact message without a contact")
	}
	return &waE2E.Message{ContactMessage: &waE2E.ContactMessage{
		DisplayName: proto.String(msg.Contact.Name),
		Vcard:       proto.String(vcard(msg.Contact)),
	}}, nil
}

// vcard renders one contact as the vCard 3.0 text WhatsApp expects.
func vcard(contact *botflota.Contact) string {
	return "BEGIN:VCARD\r\n" +
		"VERSION:3.0\r\n" +
		"FN:" + contact.Name + "\r\n" +
		"N:" + contact.Name + ";;;;\r\n" +
		"TEL;type=CELL;type=VOICE;waid=" + contact.Phone + ":+" + contact.Phone + "\r\n" +
		"END:VCARD"
}

func buildPoll(msg botflota.OutgoingMessage) (*waE2E.Message, error) {
	if msg.Poll == nil {
		return nil, errors.New("whatsapp: poll message without a poll")
	}
	if len(msg.Poll.Options) < 2 {
		return nil, errors.New("whatsapp: poll needs at least two options")
	}
	// The poll secret lets a voter's encrypted vote be read back; it travels
	// in the message context, exactly as whatsmeow's own builder does.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("whatsapp: poll secret: %w", err)
	}
	options := make([]*waE2E.PollCreationMessage_Option, len(msg.Poll.Options))
	for i, name := range msg.Poll.Options {
		options[i] = &waE2E.PollCreationMessage_Option{OptionName: proto.String(name)}
	}
	return &waE2E.Message{
		PollCreationMessage: &waE2E.PollCreationMessage{
			Name:                   proto.String(msg.Poll.Question),
			Options:                options,
			SelectableOptionsCount: proto.Uint32(selectableCount(msg.Poll)),
		},
		MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret},
	}, nil
}

// selectableCount clamps how many options a voter may pick: one by default,
// never more than the poll offers.
func selectableCount(poll *botflota.Poll) uint32 {
	count := poll.Selectable
	if count <= 0 {
		count = 1
	}
	if count > len(poll.Options) {
		count = len(poll.Options)
	}
	return uint32(count)
}

// buildMedia reads an attachment from its url or path, uploads it, and builds
// the matching wire message (ADR 0036).
func (a *Adapter) buildMedia(
	ctx context.Context, msg botflota.OutgoingMessage,
) (*waE2E.Message, error) {
	media := msg.Media
	if media == nil {
		return nil, errors.New("whatsapp: media message without media")
	}
	data, err := a.readMedia(ctx, media)
	if err != nil {
		return nil, err
	}
	mime := media.MIME
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
	resp, err := upload(ctx, data, mediaTypeFor(msg.Kind))
	if err != nil {
		return nil, fmt.Errorf("whatsapp: upload media: %w", err)
	}
	return mediaMessage(msg, mime, resp)
}

// mediaMessage fills the wire message of one media kind from an upload.
func mediaMessage(
	msg botflota.OutgoingMessage, mime string, resp whatsmeow.UploadResponse,
) (*waE2E.Message, error) {
	media := msg.Media
	switch msg.Kind {
	case botflota.KindImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			Caption: optional(media.Caption), FileLength: proto.Uint64(resp.FileLength),
			MediaKey: resp.MediaKey, FileSHA256: resp.FileSHA256,
			FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	case botflota.KindVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			Caption: optional(media.Caption), FileLength: proto.Uint64(resp.FileLength),
			MediaKey: resp.MediaKey, FileSHA256: resp.FileSHA256,
			FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	case botflota.KindAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			FileLength: proto.Uint64(resp.FileLength), MediaKey: resp.MediaKey,
			FileSHA256: resp.FileSHA256, FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	case botflota.KindDocument:
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			FileName: optional(media.Name), Title: optional(media.Name),
			Caption: optional(media.Caption), FileLength: proto.Uint64(resp.FileLength),
			MediaKey: resp.MediaKey, FileSHA256: resp.FileSHA256,
			FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	case botflota.KindSticker:
		return &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
			URL: &resp.URL, DirectPath: &resp.DirectPath, Mimetype: proto.String(mime),
			FileLength: proto.Uint64(resp.FileLength), MediaKey: resp.MediaKey,
			FileSHA256: resp.FileSHA256, FileEncSHA256: resp.FileEncSHA256,
		}}, nil
	default:
		return nil, fmt.Errorf("%w: %s", botflota.ErrUnsupportedKind, msg.Kind)
	}
}

// mediaTypeFor picks the upload key system of a media kind. Images and
// stickers share the image keys.
func mediaTypeFor(kind botflota.MessageKind) whatsmeow.MediaType {
	switch kind {
	case botflota.KindVideo:
		return whatsmeow.MediaVideo
	case botflota.KindAudio:
		return whatsmeow.MediaAudio
	case botflota.KindDocument:
		return whatsmeow.MediaDocument
	default:
		return whatsmeow.MediaImage
	}
}

// readMedia reads the attachment bytes from a url or a local path.
func (a *Adapter) readMedia(ctx context.Context, media *botflota.Media) ([]byte, error) {
	switch {
	case media.URL != "":
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, media.URL, nil)
		if err != nil {
			return nil, fmt.Errorf("whatsapp: media url: %w", err)
		}
		client := a.http
		if client == nil {
			client = http.DefaultClient
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("whatsapp: fetch media: %w", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("whatsapp: fetch media: %s", response.Status)
		}
		return readLimited(response.Body)
	case media.Path != "":
		file, err := os.Open(media.Path)
		if err != nil {
			return nil, fmt.Errorf("whatsapp: open media: %w", err)
		}
		defer file.Close()
		return readLimited(file)
	default:
		return nil, errors.New("whatsapp: media has neither url nor path")
	}
}

// readLimited reads at most maxMediaSize bytes and reports an oversized file
// instead of buffering it whole.
func readLimited(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxMediaSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMediaSize {
		return nil, fmt.Errorf("whatsapp: media exceeds %d bytes", maxMediaSize)
	}
	return data, nil
}

// clientForUpload returns the live client, or an error when the channel is not
// connected. Media needs it to upload the bytes.
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
