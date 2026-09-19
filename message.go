// Package botflota holds the core contracts shared by the engine and its
// adapters: messages, persisted records, ports, errors, the structured logger,
// and the template language (ports.go, records.go, logging.go, template.go).
// It never imports YAML, SQL, whatsmeow, or LLM SDK types (ADR 0004, ADR 0034,
// ADR 0034).
package botflota

import (
	"errors"
	"time"
)

// ErrNotFound reports a persisted record that does not exist.
var ErrNotFound = errors.New("not found")

// ErrNoLLM reports an LLM-only operation with no provider configured.
var ErrNoLLM = errors.New("no LLM configured")

// MessageKind classifies a message's payload, inbound or outbound.
type MessageKind string

const (
	KindText     MessageKind = "text"
	KindLocation MessageKind = "location"
)

// IncomingMessage is one inbound message. Sender is the resolved phone number
// in plain digits (e.g. "5215500000000"); the JID and any LID stay inside the
// channel adapter (ADR 0020). SenderName is the WhatsApp push name: best
// effort, possibly empty, and never an identifier (ADR 0029).
type IncomingMessage struct {
	Bot        string
	Sender     string
	SenderName string
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

// OutgoingMessage is one message the engine asks the channel to deliver.
// Recipient is the plain phone number the reply is for; the adapter maps it to
// the transport address (ADR 0020).
type OutgoingMessage struct {
	Bot       string
	Recipient string
	Kind      MessageKind
	Body      string
	Location  *Location
}
