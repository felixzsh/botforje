// Package botflota holds the core contracts shared by the engine and its
// adapters: messages, persisted records, ports, errors, the structured logger,
// and the template language. It never imports YAML, SQL, whatsmeow, or LLM
// SDK types (ADR 0004, ADR 0034).
package botflota

import (
	"errors"
	"time"
)

// ErrNotFound reports a persisted record that does not exist.
var ErrNotFound = errors.New("not found")

// ErrNoLLM reports an LLM-only operation with no provider configured.
var ErrNoLLM = errors.New("no LLM configured")

// ErrDuplicate reports a message whose provider id already exists in its chat;
// the history import reads it as already imported, not a failure (ADR 0026).
var ErrDuplicate = errors.New("duplicate")

// ErrUnsupportedKind reports an outbound content a channel cannot deliver; it
// answers with the error instead of silently dropping the message (ADR 0036).
var ErrUnsupportedKind = errors.New("unsupported message content")

// ErrBusy reports an operation that already has a live attempt running, such
// as a second pairing session for one bot (ADR 0025).
var ErrBusy = errors.New("already in progress")

// ErrDestinationNotEmpty reports a history migration whose destination already
// holds a chat. History is moved, never merged, so the caller empties the
// destination first or picks another bot (ADR 0042).
var ErrDestinationNotEmpty = errors.New("destination already holds history")

// ErrBotNotRunning reports an operation that needs a bot's live actor, such as
// holding one of its chats, while the bot is stopped. It is a state of the
// instance, not a failure, so a caller can wait and retry (ADR 0043).
var ErrBotNotRunning = errors.New("bot is not running")

// ErrInvalidCursor reports a list cursor a caller did not receive from the
// read that owns it (ADR 0044).
var ErrInvalidCursor = errors.New("invalid cursor")

// IncomingMessage is one inbound message. FromPhone is the resolved phone
// number in plain digits; the JID and any LID stay inside the channel adapter
// (ADR 0020). FromName is a best-effort push name, never an identifier
// (ADR 0029). ProviderMessageID is the channel's id, stored for dedup against
// a later history sync (ADR 0026).
type IncomingMessage struct {
	Bot               string
	FromPhone         string
	FromName          string
	Content           MessageContent
	ProviderMessageID string
	IsGroup           bool
	At                time.Time
}

// OutgoingMessage is one message the engine asks the channel to deliver.
// RecipientPhone is the plain phone number the adapter maps to its transport
// address (ADR 0020). Origin and Routing are the provenance recorded with the
// reply (ADR 0026). StoredMessageID is the stored row of the reply; the
// channel fills it with its own message id after delivery, closing the dedup
// loop.
type OutgoingMessage struct {
	Bot             string
	RecipientPhone  string
	Content         SendableContent
	Origin          MessageOrigin
	Routing         RoutingMethod
	StoredMessageID int64
}
