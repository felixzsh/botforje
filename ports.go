package botflota

import (
	"context"
	"time"
)

// MessageChannel is the transport port, owned by its consumer (ADR 0004).
type MessageChannel interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	// Send delivers one message and returns the channel's own message id, or
	// an empty string when the channel cannot report one. The engine records
	// the reply before it is sent and the id closes the dedup loop with a
	// later history sync (ADR 0026).
	Send(ctx context.Context, msg OutgoingMessage) (string, error)
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
// get-or-create keyed by the sender's profile; ForSender and Recent are the
// read side of the API; RecordMessage updates last activity and the inbound
// count; NoteHistory widens the activity range with imported history;
// SaveGuards persists the chat's per-action guard state (ADR 0026, ADR 0027,
// ADR 0029, ADR 0035).
type ChatStore interface {
	Ensure(ctx context.Context, scope, bot string, senderID int64, at time.Time) (*Chat, error)
	ForSender(ctx context.Context, scope, bot string, senderID int64) (*Chat, error)
	Recent(ctx context.Context, scope, bot string, limit int) ([]ChatSummary, error)
	RecordMessage(ctx context.Context, id int64, at time.Time) error
	// NoteHistory widens a chat's activity to cover imported history:
	// started_at moves back to from and updated_at moves forward to to, never
	// the other way, so newer live activity is left alone. A zero bound is
	// ignored.
	NoteHistory(ctx context.Context, chatID int64, from, to time.Time) error
	SaveGuards(ctx context.Context, chatID int64, guards map[string]GuardState) error
}

// SenderStore records the profile of each person who writes to a bot. Upsert
// creates it on first contact and updates it afterwards; it reports whether the
// profile is new, which is the first-contact hook (ADR 0029). SetDisplayName
// records a name learned from channel contact data without touching first or
// last seen, and is a no-op when there is no profile (ADR 0035).
type SenderStore interface {
	Upsert(ctx context.Context, scope, phone, name string, at time.Time) (*Sender, bool, error)
	Get(ctx context.Context, scope, phone string) (*Sender, error)
	SetDisplayName(ctx context.Context, scope, phone, name string, at time.Time) error
}

// SessionStore records a chat's session runs. Latest returns the most recent
// one, or ErrNotFound when the chat never had a session; CountActive counts
// the unexpired runs of one bot at a time, for status (ADR 0026).
type SessionStore interface {
	Latest(ctx context.Context, chatID int64) (*Session, error)
	Save(ctx context.Context, session *Session) error
	Delete(ctx context.Context, chatID int64) error
	CountActive(ctx context.Context, scope, bot string, at time.Time) (int, error)
}

// MessageStore records message history (ADR 0026). Append reports
// ErrDuplicate when the chat already holds a row with the same provider id.
// SetProviderID fills the channel id of a stored outbound message once the
// channel reports it. History returns up to limit messages of a chat, oldest
// first: the last ones when afterID is zero, or the messages newer than
// afterID otherwise. A reader shows the conversation in order and a follower
// resumes from the last id it saw.
type MessageStore interface {
	Append(ctx context.Context, message *Message) error
	SetProviderID(ctx context.Context, id int64, providerID string) error
	History(ctx context.Context, chatID, afterID int64, limit int) ([]Message, error)
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

// OwnerTokenStore stores the instance's single owner token (ADR 0025). It
// reports ErrNotFound until the API creates one on first run.
type OwnerTokenStore interface {
	Get(ctx context.Context) (*OwnerToken, error)
	Put(ctx context.Context, token *OwnerToken) error
}

// AccessTokenStore stores the scoped access tokens (ROADMAP Phase 13).
// FindByHash reports ErrNotFound for an unknown hash; Revoke reports
// ErrNotFound for an unknown id.
type AccessTokenStore interface {
	Create(ctx context.Context, token *AccessToken) error
	FindByHash(ctx context.Context, hash []byte) (*AccessToken, error)
	List(ctx context.Context) ([]AccessToken, error)
	Revoke(ctx context.Context, id int64, at time.Time) error
}
