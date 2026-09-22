package botflota

import (
	"context"
	"time"
)

// MessageChannel is the transport port, owned by its consumer (ADR 0004).
type MessageChannel interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	// Send delivers one message and returns the channel's message id, or empty
	// when the channel cannot report one. The reply is stored before sending,
	// so the id closes the dedup loop with a later history sync (ADR 0026).
	Send(ctx context.Context, msg OutgoingMessage) (string, error)
	Incoming() <-chan IncomingMessage
}

// StateReporter is an optional MessageChannel capability: a channel that can
// report its lifecycle for status (ADR 0011).
type StateReporter interface {
	ChannelState() string
}

// HistorySyncer is an optional MessageChannel capability: asking the primary
// device for older messages of one chat over the live connection (ADR 0035).
type HistorySyncer interface {
	SyncHistory(
		ctx context.Context, phone string, count int, timeout time.Duration,
	) (HistoryResult, error)
}

// HistoryResult reports one chat's on-demand history backfill (ADR 0035).
// Received counts the provider entries the phone answered with, Imported what
// the store kept, and Stable and Exhausted whether older pages remain.
type HistoryResult struct {
	Received  int
	Rounds    int
	Imported  int
	Duplicate int
	Skipped   int
	Stable    bool
	Exhausted bool
}

// LLMProvider classifies unmatched input and realizes replies (ADR 0032).
type LLMProvider interface {
	Classify(ctx context.Context, req ClassifyRequest) (ClassifyResult, error)
	Realize(ctx context.Context, req RealizeRequest) (string, error)
}

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

type RealizeRequest struct {
	Intent string
	Policy string
	Anchor string
	Text   string
}

// NullLLM is the deterministic provider for a bot with no `llm` (ADR 0031):
// it never classifies and refuses to realize.
type NullLLM struct{}

func (NullLLM) Classify(context.Context, ClassifyRequest) (ClassifyResult, error) {
	return ClassifyResult{}, nil
}

func (NullLLM) Realize(context.Context, RealizeRequest) (string, error) {
	return "", ErrNoLLM
}

// Store ports: the core package owns the interfaces and an adapter
// implements them (ADR 0006). The persisted shapes live in records.go.

// ChatStore records the durable chat with each person (ADR 0026, ADR 0027,
// ADR 0029, ADR 0035). Ensure is a get-or-create keyed by the sender's
// profile.
type ChatStore interface {
	Ensure(ctx context.Context, scope, bot string, senderID int64, at time.Time) (*Chat, error)
	ForSender(ctx context.Context, scope, bot string, senderID int64) (*Chat, error)
	// Recent returns the most recently active chats first. A zero limit means
	// every chat, which is how `chat sync --all` walks them (ADR 0035).
	Recent(ctx context.Context, scope, bot string, limit int) ([]ChatSummary, error)
	RecordMessage(ctx context.Context, id int64, at time.Time) error
	// NoteHistory widens a chat's activity to cover imported history:
	// started_at moves back to from and updated_at forward to to, never the
	// other way. A zero bound is ignored.
	NoteHistory(ctx context.Context, chatID int64, from, to time.Time) error
	SaveGuards(ctx context.Context, chatID int64, guards map[string]GuardState) error
}

// SenderStore records the profile of each person who writes to a bot. Upsert
// reports whether the profile is new, the first-contact hook (ADR 0029).
// SetDisplayName records a name from channel contact data without touching
// first or last seen (ADR 0035).
type SenderStore interface {
	Upsert(ctx context.Context, scope, phone, name string, at time.Time) (*Sender, bool, error)
	Get(ctx context.Context, scope, phone string) (*Sender, error)
	SetDisplayName(ctx context.Context, scope, phone, name string, at time.Time) error
}

// ContactStore records the address-book entries of each bot's linked account
// when contact import is on (ADR 0037). Put merges the non-empty name fields
// and never clears one; List returns the most recently updated entries first,
// and a zero limit means every one. It is optional for a channel with no
// address book (ADR 0029).
type ContactStore interface {
	Put(ctx context.Context, contact *Contact) error
	List(ctx context.Context, scope, bot string, limit int) ([]Contact, error)
}

// SessionStore records a chat's session runs (ADR 0026). Save inserts a run
// when Session.ID is zero and updates it otherwise. Delete drops every run of
// a chat and keeps its message history. CountActive counts the unexpired runs
// of one bot at a time; a run without expiry never expires.
type SessionStore interface {
	Latest(ctx context.Context, chatID int64) (*Session, error)
	Save(ctx context.Context, session *Session) error
	Delete(ctx context.Context, chatID int64) error
	CountActive(ctx context.Context, scope, bot string, at time.Time) (int, error)
}

// MessageStore records message history (ADR 0026). Append reports
// ErrDuplicate when the chat already holds the same provider id, and
// SetProviderID fills the channel id of a stored reply once reported. Oldest
// returns the oldest message that can anchor a history request — one with a
// channel id, never a call row (ADR 0035). History returns up to limit
// messages oldest first: the last ones when afterID is zero, or those newer
// than afterID.
type MessageStore interface {
	Append(ctx context.Context, message *Message) error
	SetProviderID(ctx context.Context, id int64, providerID string) error
	Oldest(ctx context.Context, chatID int64) (*Message, error)
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

// AccessTokenStore stores the scoped access tokens, hash only (ROADMAP Phase
// 13). FindByHash returns a revoked or expired token too; the caller checks
// Active, and List omits hashes. Revoke is idempotent and reports ErrNotFound
// for an unknown id.
type AccessTokenStore interface {
	Create(ctx context.Context, token *AccessToken) error
	FindByHash(ctx context.Context, hash []byte) (*AccessToken, error)
	List(ctx context.Context) ([]AccessToken, error)
	Revoke(ctx context.Context, id int64, at time.Time) error
}

// PairingRunner drives the live pairing session of one bot, so the CLI and a
// dashboard link an account through the same path (ADR 0025). StartPair
// answers ErrBusy while a session for that bot still runs, CurrentPair
// answers ErrNotFound when there is none, and CancelPair is idempotent once a
// session is final.
type PairingRunner interface {
	StartPair(
		ctx context.Context, scope, bot string, req PairRequest,
	) (*PairingSession, error)
	CurrentPair(ctx context.Context, scope, bot string) (*PairingSession, error)
	CancelPair(ctx context.Context, scope, bot string) error
}
