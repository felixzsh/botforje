// Package botflota holds the core contracts shared by the engine and its
// adapters: messages, persisted records, ports, errors, the structured logger,
// and the template language. It never imports YAML, SQL, or messaging or LLM
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

// ErrDuplicate reports a message whose provider id already exists for its
// destination; the history import reads it as already imported, not a failure
// (ADR 0026).
var ErrDuplicate = errors.New("duplicate")

// ErrUnsupportedKind reports an outbound content a channel cannot deliver; it
// answers with the error instead of silently dropping the message (ADR 0036).
var ErrUnsupportedKind = errors.New("unsupported message content")

// ErrBusy reports an operation that already has a live attempt running, such
// as a second pairing session for one bot (ADR 0025).
var ErrBusy = errors.New("already in progress")

// ErrIdentityInUse reports a verified identity already linked to another bot,
// which one instance refuses (ADR 0021). The conflict may sit in a scope the
// caller cannot see, so the text a caller gets names neither the other bot nor
// its scope; the diagnostic detail goes to the instance log.
var ErrIdentityInUse = errors.New("identity is already linked to a bot")

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

// ErrParticipantConflict reports a chat whose known participant differs from
// the identity explicitly supplied now. The association is never silently
// rewritten (ADR 0051).
var ErrParticipantConflict = errors.New("chat already has a different participant")

// ErrConflict reports a setup request that contradicts the session it
// targets: a stale revision, a request key already used for different
// parameters, or an input the session's method does not accept (ADR 0053).
// A stale session id is not a conflict; it answers ErrNotFound and stays
// inert, so a retry can never affect a replacement.
var ErrConflict = errors.New("conflicting setup request")

// IncomingMessage is one inbound message. Sender is the resolved comparable
// identity; the adapter normalizes it and never assigns a fabricated peer
// (ADR 0051). Target addresses the conversation it arrived in. FromName is a
// best-effort push name, never an identifier (ADR 0029). ProviderMessageID is
// the channel's id, stored for dedup against a later history sync (ADR 0026).
type IncomingMessage struct {
	Bot               string
	Sender            IdentityKey
	Target            ChatTarget
	FromName          string
	Content           MessageContent
	ProviderMessageID string
	// MessageSpace is the adapter-declared message-ID space the id
	// belongs to, taken from the adapter's own account/profile
	// knowledge (ADR 0026, ADR 0051). The engine stores it with the
	// message, so dedup never confuses one account's ids with another's.
	MessageSpace string
	// ReceiveCursor is the opaque source update id or receive cursor
	// that settles this entry's processing completion (ADR 0004,
	// ADR 0040). It is never part of message identity and never feeds
	// dedup: the durable key stays the provider id in its message
	// space. Empty when the source confirms nothing.
	ReceiveCursor string
	At            time.Time
}

// OutgoingMessage is one message the engine asks the channel to deliver.
// Target is the exact conversation the adapter addresses. Origin and Routing
// are the provenance recorded with the reply (ADR 0026). StoredMessageID is
// the stored row of the reply; the channel fills it with its own message id
// after delivery, closing the dedup loop.
type OutgoingMessage struct {
	Bot             string
	Target          ChatTarget
	Content         SendableContent
	Origin          MessageOrigin
	Routing         RoutingMethod
	StoredMessageID int64
	// DeliveryKey is the delivery-owned idempotency key, assigned when the
	// reply is queued and reused across every attempt of the same durable
	// delivery, so a retry never mints a new provider id (ADR 0052).
	DeliveryKey string
}

// ExternalSend is a message that left a bot's binding without Botflota
// sending it: a human replied from another client, or another automation on the
// same account sent it (ADR 0043). It carries the exact
// destination, never a raw device locator. It is an observation, not a
// routable turn.
type ExternalSend struct {
	Bot               string
	Target            ChatTarget
	Content           MessageContent
	ProviderMessageID string
	// MessageSpace is the adapter-declared message-ID space the id
	// belongs to, taken from the adapter's own account/profile
	// knowledge (ADR 0026, ADR 0051).
	MessageSpace string
	At           time.Time
}
