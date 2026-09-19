package botflota

import (
	"context"
	"time"
)

// MessageChannel is the transport port, owned by its consumer (ADR 0004).
type MessageChannel interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Send(ctx context.Context, msg OutgoingMessage) error
	Incoming() <-chan IncomingMessage
}

// StateReporter is an optional MessageChannel capability: a channel that can
// report its lifecycle for status (ADR 0011).
type StateReporter interface {
	ChannelState() string
}

// LLMProvider classifies unmatched input and realizes replies (ADR 0032).
// Phase 4 defines the port with a null implementation; Phase 8 adds real
// providers and may refine the request shapes.
type LLMProvider interface {
	Classify(ctx context.Context, req ClassifyRequest) (ClassifyResult, error)
	Realize(ctx context.Context, req RealizeRequest) (string, error)
}

// ClassifyRequest asks which edge intent, if any, fits the text.
type ClassifyRequest struct {
	Goal    string
	Intents []string
	Text    string
}

// ClassifyResult is a classification; OK is false when nothing fits.
type ClassifyResult struct {
	Intent string
	OK     bool
}

// RealizeRequest asks for reply text anchored on what is already known.
type RealizeRequest struct {
	Intent string
	Policy string
	Anchor string
	Text   string
}

// NullLLM is the deterministic provider for a bot with no `llm` (ADR 0031):
// it never classifies and refuses to realize.
type NullLLM struct{}

// Classify always reports no match.
func (NullLLM) Classify(context.Context, ClassifyRequest) (ClassifyResult, error) {
	return ClassifyResult{}, nil
}

// Realize always fails; an intent-only message needs a real provider.
func (NullLLM) Realize(context.Context, RealizeRequest) (string, error) {
	return "", ErrNoLLM
}

// Store ports: the core package owns the interfaces and an adapter
// implements them (ADR 0006). The persisted shapes live in records.go.

// ChatStore records the durable chat with each person. Ensure is a
// get-or-create keyed by the sender's profile; RecordMessage updates last
// activity and the inbound count; SaveGuards persists the chat's per-action
// guard state (ADR 0026, ADR 0027, ADR 0029).
type ChatStore interface {
	Ensure(ctx context.Context, scope, bot string, senderID int64, at time.Time) (*Chat, error)
	RecordMessage(ctx context.Context, id int64, at time.Time) error
	SaveGuards(ctx context.Context, chatID int64, guards map[string]GuardState) error
}

// SenderStore records the profile of each person who writes to a bot. Upsert
// creates it on first contact and updates it afterwards; it reports whether the
// profile is new, which is the first-contact hook (ADR 0029).
type SenderStore interface {
	Upsert(ctx context.Context, scope, phone, name string, at time.Time) (*Sender, bool, error)
	Get(ctx context.Context, scope, phone string) (*Sender, error)
}

// SessionStore records a chat's session runs. Latest returns the most recent
// one, or ErrNotFound when the chat never had a session (ADR 0026).
type SessionStore interface {
	Latest(ctx context.Context, chatID int64) (*Session, error)
	Save(ctx context.Context, session *Session) error
	Delete(ctx context.Context, chatID int64) error
}

// MessageStore records message history (ADR 0026).
type MessageStore interface {
	Append(ctx context.Context, message *Message) error
	List(ctx context.Context, chatID int64, limit int) ([]Message, error)
}

// LinkedAccountStore records the channel identity linked to each bot
// (ADR 0006). Pairing is tracked separately by PairingStore.
type LinkedAccountStore interface {
	Get(ctx context.Context, scope, bot string) (*LinkedAccount, error)
	List(ctx context.Context) ([]LinkedAccount, error)
	Put(ctx context.Context, account *LinkedAccount) error
	Delete(ctx context.Context, scope, bot string) error
}

// PairingStore records the last confirmed pairing transition of a bot
// (ADR 0011).
type PairingStore interface {
	Get(ctx context.Context, scope, bot string) (*PairingStatus, error)
	Set(ctx context.Context, status *PairingStatus) error
}
