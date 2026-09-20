package botflota

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// MessageKind classifies a message's payload in a channel-neutral way. It is
// the shared vocabulary of every channel: not every channel can produce or
// accept every kind, and an adapter implements the subset it supports
// (ADR 0036). A new wire type a channel does not recognize is KindUnknown, so
// the message still has an anchor instead of being dropped.
type MessageKind string

const (
	KindText MessageKind = "text"
	// KindImage, KindVideo, KindAudio, KindDocument, and KindSticker are the
	// media kinds: the channel carries the bytes, and only the caption or file
	// name travels in a persisted row.
	KindImage    MessageKind = "image"
	KindVideo    MessageKind = "video"
	KindAudio    MessageKind = "audio"
	KindDocument MessageKind = "document"
	KindSticker  MessageKind = "sticker"
	// KindLocation is a static map pin or a live-location share.
	KindLocation MessageKind = "location"
	// KindContact is one shared contact or a contact list.
	KindContact MessageKind = "contact"
	KindPoll    MessageKind = "poll"
	KindEvent   MessageKind = "event"
	// KindInteractive is an answer to buttons, a list, or a template: the
	// picked option travels as the visible text.
	KindInteractive MessageKind = "interactive"
	// KindCommerce is a catalog, order, or invoice message.
	KindCommerce MessageKind = "commerce"
	// KindCall is a call attempt or call log entry. The engine never creates
	// it; it is not a live message (ADR 0035).
	KindCall MessageKind = "call"
	// KindUnknown is a message a channel recognized as content but cannot
	// classify further. It is kept so the chat has an anchor (ADR 0035).
	KindUnknown MessageKind = "unknown"
)

// MessageContent is the payload of a message, whatever its kind. The unexported
// method seals the set to this package, so a channel and the engine switch over
// a known list instead of guessing at a wire shape. VisibleText is the text a
// reader shows and the engine routes on; it may be empty for a binary payload
// (ADR 0036).
type MessageContent interface {
	MessageKind() MessageKind
	VisibleText() string
	messageContent()
}

// SendableContent is content an action may emit. A kind the engine only reads
// from a channel — a call log, an event, a commerce message, an unrecognized
// payload — is a MessageContent but not a SendableContent, so the compiler
// rejects an attempt to send it (ADR 0036).
type SendableContent interface {
	MessageContent
	sendableContent()
}

// MediaType names the media kinds, so a MediaContent cannot claim a non-media
// kind. The values match the MessageKind constants of the same name.
type MediaType string

const (
	MediaImage    MediaType = "image"
	MediaVideo    MediaType = "video"
	MediaAudio    MediaType = "audio"
	MediaDocument MediaType = "document"
	MediaSticker  MediaType = "sticker"
)

// Valid reports whether the type is one of the media kinds.
func (t MediaType) Valid() bool {
	switch t {
	case MediaImage, MediaVideo, MediaAudio, MediaDocument, MediaSticker:
		return true
	default:
		return false
	}
}

// TextContent is a plain text message.
type TextContent struct {
	Body string `json:"body"`
}

// MessageKind reports the kind.
func (TextContent) MessageKind() MessageKind { return KindText }

// VisibleText returns the body.
func (c TextContent) VisibleText() string { return c.Body }

func (TextContent) messageContent()  {}
func (TextContent) sendableContent() {}

// MediaContent is an attachment. The channel reads the bytes from URL or Path,
// never the engine, so a secret and a large file stay out of the runtime
// context; an inbound attachment carries neither and only its metadata.
type MediaContent struct {
	Type    MediaType `json:"type"`
	URL     string    `json:"url,omitempty"`
	Path    string    `json:"path,omitempty"`
	Name    string    `json:"name,omitempty"`
	Caption string    `json:"caption,omitempty"`
	MIME    string    `json:"mime,omitempty"`
}

// MessageKind reports the media kind.
func (c MediaContent) MessageKind() MessageKind { return MessageKind(c.Type) }

// VisibleText returns the caption, or the file name when there is none.
func (c MediaContent) VisibleText() string {
	if c.Caption != "" {
		return c.Caption
	}
	return c.Name
}

func (MediaContent) messageContent()  {}
func (MediaContent) sendableContent() {}

// LocationContent is a map pin, static or live.
type LocationContent struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Name        string  `json:"name,omitempty"`
	Address     string  `json:"address,omitempty"`
	URL         string  `json:"url,omitempty"`
	Description string  `json:"description,omitempty"`
}

// MessageKind reports the kind.
func (LocationContent) MessageKind() MessageKind { return KindLocation }

// VisibleText returns the pin's name, its description, or its address.
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

// ContactContent is one shared contact card.
type ContactContent struct {
	Name  string `json:"name"`
	Phone string `json:"phone,omitempty"`
}

// MessageKind reports the kind.
func (ContactContent) MessageKind() MessageKind { return KindContact }

// VisibleText returns the contact's name.
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

// MessageKind reports the kind.
func (PollContent) MessageKind() MessageKind { return KindPoll }

// VisibleText returns the poll question.
func (c PollContent) VisibleText() string { return c.Question }

func (PollContent) messageContent()  {}
func (PollContent) sendableContent() {}

// EventContent is a calendar event.
type EventContent struct {
	Name string `json:"name"`
}

// MessageKind reports the kind.
func (EventContent) MessageKind() MessageKind { return KindEvent }

// VisibleText returns the event name.
func (c EventContent) VisibleText() string { return c.Name }

func (EventContent) messageContent() {}

// InteractiveContent is an answer to buttons, a list, or a template.
type InteractiveContent struct {
	Text string `json:"text"`
}

// MessageKind reports the kind.
func (InteractiveContent) MessageKind() MessageKind { return KindInteractive }

// VisibleText returns the picked option.
func (c InteractiveContent) VisibleText() string { return c.Text }

func (InteractiveContent) messageContent() {}

// CommerceContent is a catalog, order, or invoice message.
type CommerceContent struct {
	Title string `json:"title"`
}

// MessageKind reports the kind.
func (CommerceContent) MessageKind() MessageKind { return KindCommerce }

// VisibleText returns the commerce title.
func (c CommerceContent) VisibleText() string { return c.Title }

func (CommerceContent) messageContent() {}

// CallContent is a call attempt or call log entry.
type CallContent struct {
	Label string `json:"label"`
}

// MessageKind reports the kind.
func (CallContent) MessageKind() MessageKind { return KindCall }

// VisibleText returns the call label.
func (c CallContent) VisibleText() string { return c.Label }

func (CallContent) messageContent() {}

// UnknownContent is content a channel recognized but could not classify. It
// carries no payload; the kind and the channel id are the anchor (ADR 0035).
type UnknownContent struct{}

// MessageKind reports the kind.
func (UnknownContent) MessageKind() MessageKind { return KindUnknown }

// VisibleText returns an empty string.
func (UnknownContent) VisibleText() string { return "" }

func (UnknownContent) messageContent() {}

// ContentKind returns content's kind, or an empty kind when content is nil, so
// a caller that only reads a label never dereferences a missing payload.
func ContentKind(content MessageContent) MessageKind {
	if content == nil {
		return ""
	}
	return content.MessageKind()
}

// ContentText returns content's visible text, or an empty string when content
// is nil.
func ContentText(content MessageContent) string {
	if content == nil {
		return ""
	}
	return content.VisibleText()
}

// ContentJSON encodes content for storage. The kind and the visible text are
// written beside it as derived columns for filtering and search (ADR 0036).
func ContentJSON(content MessageContent) ([]byte, error) {
	if content == nil {
		return nil, errors.New("nil message content")
	}
	return json.Marshal(content)
}

// ContentFromJSON rebuilds content of the given kind. Empty or `{}` data is a
// row written before the structured column existed, so it falls back to the
// kind and the visible text (ADR 0036).
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

// contentFromText rebuilds content from a row that kept only its kind and its
// visible text (ADR 0036).
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
