// Package botflota holds the core contracts shared by the engine and its
// adapters: messages, persisted records, ports, errors, the structured logger,
// and the template language (ports.go, records.go, logging.go, template.go,
// content.go). It never imports YAML, SQL, whatsmeow, or LLM SDK types
// (ADR 0004, ADR 0034).
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

// ErrUnsupportedKind reports an outbound content a channel cannot deliver. The
// engine never produces one; a channel with a narrower vocabulary than the
// shared set answers with it instead of silently dropping the message
// (ADR 0036).
var ErrUnsupportedKind = errors.New("unsupported message content")

// IncomingMessage is one inbound message. SenderPhone is the resolved phone
// number in plain digits (e.g. "5215500000000"); the JID and any LID stay
// inside the channel adapter (ADR 0020). SenderDisplayName is the WhatsApp push
// name: best effort, possibly empty, and never an identifier (ADR 0029).
// Content is the classified payload, whose visible text may be empty for a
// binary message (ADR 0036). ProviderMessageID is the channel's own id, which
// the engine stores for dedup against a later history sync (ADR 0026).
type IncomingMessage struct {
	Bot               string
	SenderPhone       string
	SenderDisplayName string
	Content           MessageContent
	ProviderMessageID string
	IsGroup           bool
	At                time.Time
}

// OutgoingMessage is one message the engine asks the channel to deliver.
// RecipientPhone is the plain phone number the reply is for; the adapter maps
// it to the transport address (ADR 0020). Content is exactly one sendable
// payload, so a kind the engine only reads can never be sent (ADR 0036).
// Origin and Routing carry the provenance the engine records with the reply
// (ADR 0026). StoredMessageID is the stored history row the engine created for
// the reply; the channel reports its own message id after delivery so the row
// can be deduplicated against a later history sync.
type OutgoingMessage struct {
	Bot             string
	RecipientPhone  string
	Content         SendableContent
	Origin          MessageOrigin
	Routing         RoutingMethod
	StoredMessageID int64
}
