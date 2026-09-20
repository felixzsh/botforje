// Package botflota holds the core contracts shared by the engine and its
// adapters: messages, persisted records, ports, errors, the structured logger,
// and the template language (ports.go, records.go, logging.go, template.go).
// It never imports YAML, SQL, whatsmeow, or LLM SDK types (ADR 0004, ADR 0034).
package botflota

import (
	"errors"
	"time"
)

// ErrNotFound reports a persisted record that does not exist.
var ErrNotFound = errors.New("not found")

// ErrNoLLM reports an LLM-only operation with no provider configured.
var ErrNoLLM = errors.New("no LLM configured")

// ErrDuplicate reports a message whose provider id already exists in its chat.
// The history import treats it as an already-imported message, not a failure
// (ADR 0026, ADR 0035).
var ErrDuplicate = errors.New("duplicate")

// ErrUnsupportedKind reports an outbound kind a channel cannot deliver. The
// engine never produces one; a channel with a narrower vocabulary than the
// shared set answers with it instead of silently dropping the message
// (ADR 0036).
var ErrUnsupportedKind = errors.New("unsupported message kind")

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
	// picked option travels as the body.
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

// IncomingMessage is one inbound message. Sender is the resolved phone number
// in plain digits (e.g. "5215500000000"); the JID and any LID stay inside the
// channel adapter (ADR 0020). SenderName is the WhatsApp push name: best
// effort, possibly empty, and never an identifier (ADR 0029). Kind is the
// payload class and Body its visible text (caption, name, question), which may
// be empty for a purely binary message (ADR 0036).
type IncomingMessage struct {
	Bot        string
	Sender     string
	SenderName string
	Kind       MessageKind
	Body       string
	ID         string
	IsGroup    bool
	At         time.Time
}

// Location is a map pin sent as its own message.
type Location struct {
	Latitude    float64
	Longitude   float64
	Name        string
	Address     string
	URL         string
	Description string
}

// Media is an outbound attachment. URL and Path are the two sources: a channel
// reads the bytes from one of them, never the engine, so a secret and a large
// file stay out of the runtime context. Kind on the message (image, video,
// audio, document, or sticker) says how to deliver it; MIME overrides the
// detected content type.
type Media struct {
	URL     string
	Path    string
	Name    string
	Caption string
	MIME    string
}

// Contact is one shared contact card, delivered as a vCard by the channel.
type Contact struct {
	Name  string
	Phone string
}

// Poll is a poll a channel can create. Selectable is how many options a voter
// may pick; zero means one.
type Poll struct {
	Question   string
	Options    []string
	Selectable int
}

// OutgoingMessage is one message the engine asks the channel to deliver.
// Recipient is the plain phone number the reply is for; the adapter maps it to
// the transport address (ADR 0020). Kind selects the payload and exactly one
// of Body, Location, Media, Contact, or Poll carries it. Origin and Routing
// carry the provenance the engine records with the reply (ADR 0026). RecordID
// is the stored history row the engine created for the reply; the channel
// reports its own message id after delivery so the row can be deduplicated
// against a later history sync.
type OutgoingMessage struct {
	Bot       string
	Recipient string
	Kind      MessageKind
	Body      string
	Location  *Location
	Media     *Media
	Contact   *Contact
	Poll      *Poll
	Origin    MessageOrigin
	Routing   RoutingMethod
	RecordID  int64
}
