package botflota

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// MessageKind classifies a message's payload in a channel-neutral way: the
// shared vocabulary of every channel, each of which implements the subset it
// supports (ADR 0036).
type MessageKind string

const (
	KindText MessageKind = "text"
	// The media kinds: the channel carries the bytes, so only the caption or
	// file name travels in a persisted row.
	KindImage    MessageKind = "image"
	KindVideo    MessageKind = "video"
	KindAudio    MessageKind = "audio"
	KindDocument MessageKind = "document"
	KindSticker  MessageKind = "sticker"
	KindLocation MessageKind = "location"
	KindContact  MessageKind = "contact"
	KindPoll     MessageKind = "poll"
	KindEvent    MessageKind = "event"
	// KindInteractive is an answer to buttons, a list, or a template.
	KindInteractive MessageKind = "interactive"
	KindCommerce    MessageKind = "commerce"
	// KindCall is a call attempt or call log entry; the engine never creates
	// one (ADR 0035).
	KindCall MessageKind = "call"
	// KindUnknown is content a channel recognized but cannot classify; it is
	// kept so the chat has an anchor (ADR 0035).
	KindUnknown MessageKind = "unknown"
)

// MessageContent is the payload of a message. The unexported method seals the
// set to this package (ADR 0036).
type MessageContent interface {
	MessageKind() MessageKind
	VisibleText() string
	messageContent()
}

// SendableContent is content an action may emit; a kind the engine only reads
// is a MessageContent but not a SendableContent, so the compiler rejects an
// attempt to send it (ADR 0036).
type SendableContent interface {
	MessageContent
	sendableContent()
}

// MediaType names the media kinds, so a MediaContent cannot claim a non-media
// kind.
type MediaType string

const (
	MediaImage    MediaType = "image"
	MediaVideo    MediaType = "video"
	MediaAudio    MediaType = "audio"
	MediaDocument MediaType = "document"
	MediaSticker  MediaType = "sticker"
)

func (t MediaType) Valid() bool {
	switch t {
	case MediaImage, MediaVideo, MediaAudio, MediaDocument, MediaSticker:
		return true
	default:
		return false
	}
}

type TextContent struct {
	Body string `json:"body"`
}

func (TextContent) MessageKind() MessageKind { return KindText }

func (c TextContent) VisibleText() string { return c.Body }

func (TextContent) messageContent()  {}
func (TextContent) sendableContent() {}

// MediaContent is an attachment. The channel reads the bytes from URL or Path,
// never the engine, so a secret or a large file stays out of the runtime
// context; an inbound attachment carries only its metadata.
type MediaContent struct {
	Type    MediaType `json:"type"`
	URL     string    `json:"url,omitempty"`
	Path    string    `json:"path,omitempty"`
	Name    string    `json:"name,omitempty"`
	Caption string    `json:"caption,omitempty"`
	MIME    string    `json:"mime,omitempty"`
}

func (c MediaContent) MessageKind() MessageKind { return MessageKind(c.Type) }

func (c MediaContent) VisibleText() string {
	if c.Caption != "" {
		return c.Caption
	}
	return c.Name
}

func (MediaContent) messageContent()  {}
func (MediaContent) sendableContent() {}

type LocationContent struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Name        string  `json:"name,omitempty"`
	Address     string  `json:"address,omitempty"`
	URL         string  `json:"url,omitempty"`
	Description string  `json:"description,omitempty"`
}

func (LocationContent) MessageKind() MessageKind { return KindLocation }

func (c LocationContent) VisibleText() string {
	switch {
	case c.Name != "":
		return c.Name
	case c.Description != "":
		return c.Description
	default:
		return c.Address
	}
}

func (LocationContent) messageContent()  {}
func (LocationContent) sendableContent() {}

type ContactContent struct {
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
}

func (ContactContent) MessageKind() MessageKind { return KindContact }

func (c ContactContent) VisibleText() string { return c.Name }

func (ContactContent) messageContent()  {}
func (ContactContent) sendableContent() {}

// PollContent is a poll. Selectable is how many options a voter may pick; zero
// means one.
type PollContent struct {
	Question   string   `json:"question"`
	Options    []string `json:"options,omitempty"`
	Selectable int      `json:"selectable,omitempty"`
}

func (PollContent) MessageKind() MessageKind { return KindPoll }

func (c PollContent) VisibleText() string { return c.Question }

func (PollContent) messageContent()  {}
func (PollContent) sendableContent() {}

type EventContent struct {
	Name string `json:"name"`
}

func (EventContent) MessageKind() MessageKind { return KindEvent }

func (c EventContent) VisibleText() string { return c.Name }

func (EventContent) messageContent() {}

type InteractiveContent struct {
	Text string `json:"text"`
}

func (InteractiveContent) MessageKind() MessageKind { return KindInteractive }

func (c InteractiveContent) VisibleText() string { return c.Text }

func (InteractiveContent) messageContent() {}

type CommerceContent struct {
	Title string `json:"title"`
}

func (CommerceContent) MessageKind() MessageKind { return KindCommerce }

func (c CommerceContent) VisibleText() string { return c.Title }

func (CommerceContent) messageContent() {}

type CallContent struct {
	Label string `json:"label"`
}

func (CallContent) MessageKind() MessageKind { return KindCall }

func (c CallContent) VisibleText() string { return c.Label }

func (CallContent) messageContent() {}

type UnknownContent struct{}

func (UnknownContent) MessageKind() MessageKind { return KindUnknown }

func (UnknownContent) VisibleText() string { return "" }

func (UnknownContent) messageContent() {}

// ContentKind returns content's kind, or an empty kind when content is nil.
func ContentKind(content MessageContent) MessageKind {
	if content == nil {
		return ""
	}
	return content.MessageKind()
}

func ContentText(content MessageContent) string {
	if content == nil {
		return ""
	}
	return content.VisibleText()
}

// ContentJSON encodes content for storage; the store writes the kind and the
// visible text beside it as derived columns (ADR 0036).
func ContentJSON(content MessageContent) ([]byte, error) {
	if content == nil {
		return nil, errors.New("nil message content")
	}
	return json.Marshal(content)
}

// ContentFromJSON rebuilds content of the given kind. Empty or `{}` data is a
// row from before the structured column existed, so it falls back to the kind
// and the visible text (ADR 0036).
func ContentFromJSON(kind MessageKind, text string, data []byte) (MessageContent, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) {
		return contentFromText(kind, text), nil
	}
	switch kind {
	case KindText:
		return decodeContent[TextContent](data)
	case KindImage, KindVideo, KindAudio, KindDocument, KindSticker:
		return decodeContent[MediaContent](data)
	case KindLocation:
		return decodeContent[LocationContent](data)
	case KindContact:
		return decodeContent[ContactContent](data)
	case KindPoll:
		return decodeContent[PollContent](data)
	case KindEvent:
		return decodeContent[EventContent](data)
	case KindInteractive:
		return decodeContent[InteractiveContent](data)
	case KindCommerce:
		return decodeContent[CommerceContent](data)
	case KindCall:
		return decodeContent[CallContent](data)
	case KindUnknown:
		return decodeContent[UnknownContent](data)
	default:
		return nil, fmt.Errorf("unknown message kind %q", kind)
	}
}

func decodeContent[T MessageContent](data []byte) (MessageContent, error) {
	var content T
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, err
	}
	return content, nil
}

func contentFromText(kind MessageKind, text string) MessageContent {
	switch kind {
	case KindText:
		return TextContent{Body: text}
	case KindImage, KindVideo, KindAudio, KindDocument, KindSticker:
		return MediaContent{Type: MediaType(kind), Caption: text}
	case KindLocation:
		return LocationContent{Name: text}
	case KindContact:
		return ContactContent{Name: text}
	case KindPoll:
		return PollContent{Question: text}
	case KindEvent:
		return EventContent{Name: text}
	case KindInteractive:
		return InteractiveContent{Text: text}
	case KindCommerce:
		return CommerceContent{Title: text}
	case KindCall:
		return CallContent{Label: text}
	default:
		return UnknownContent{}
	}
}
