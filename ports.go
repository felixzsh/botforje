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

// DevicePresence reports whether a linked account's local channel
// credentials still exist, without exposing the device store or the transport
// addressing to its consumers (ADR 0020, ADR 0039).
type DevicePresence interface {
	HasCredentials(ctx context.Context, account *LinkedAccount) (bool, error)
}

// AccountUnpairer removes a linked account and its local credentials, the
// destructive counterpart of DevicePresence (ADR 0021, ADR 0039). The caller
// stops the running bot first, so no channel holds the connection while its
// credentials go away.
type AccountUnpairer interface {
	UnpairAccount(ctx context.Context, scope, bot string) error
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

// BotStatus is one bot's live report for the control plane: its connection
// state and outbox depth (ADR 0011, ADR 0039).
type BotStatus struct {
	Scope       string
	Bot         string
	State       string
	OutboxDepth int
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
	HasActiveSessionOutsideNodes(
		ctx context.Context, scope, bot string, nodes []string, at time.Time,
	) (bool, error)
}

// MessageStore records message history (ADR 0026). Append reports
// ErrDuplicate when the chat already holds the same provider id; the outbox
// records the channel id of a delivered reply when it marks it sent. Oldest
// returns the oldest message that can anchor a history request — one with a
// channel id, never a call row (ADR 0035). History returns up to limit
// messages oldest first: the last ones when afterID is zero, or those newer
// than afterID. HasProviderID is the cheap read the engine uses before it
// runs a turn, so a redelivery never repeats an action.
type MessageStore interface {
	Append(ctx context.Context, message *Message) error
	Oldest(ctx context.Context, chatID int64) (*Message, error)
	History(ctx context.Context, chatID, afterID int64, limit int) ([]Message, error)
	HasProviderID(ctx context.Context, chatID int64, providerID string) (bool, error)
}

// TurnRecord is one message turn's writes: the session it advanced, the
// inbound message, the replies it produced, the chat activity, and the guard
// state it changed (ADR 0026, ADR 0027). Scope, Bot, Session, and Inbound are
// required; CommitTurn links the messages to the session it saved, reports
// their stored ids back through them, and queues every reply for delivery in
// the same unit, so a crash never leaves a stored reply nobody sends.
type TurnRecord struct {
	Scope    string
	Bot      string
	Session  *Session
	ChatID   int64
	At       time.Time
	Inbound  *Message
	Outbound []OutboundRecord
	// Guards is nil when the turn changed no guard state.
	Guards map[string]GuardState
}

// OutboundRecord is one reply of the turn: the stored message and the phone
// number it goes to, which may differ from the chat's sender when a step
// overrides the recipient.
type OutboundRecord struct {
	Message   *Message
	Recipient string
}

// TurnStore commits one message turn as one unit: either the whole turn
// lands — session, messages, chat activity, guards, and queued replies — or
// none of it does. A partial turn would leave a session advanced with lost
// replies, which is why the engine never writes those shapes directly (ADR
// 0026, ADR 0027, ADR 0011).
type TurnStore interface {
	CommitTurn(ctx context.Context, turn TurnRecord) error
}

// OutboxEntry is one queued reply: the stored message id, the recipient, and
// the sendable content, read back from the queue (ADR 0011).
type OutboxEntry struct {
	ID        int64
	Attempts  int
	Recipient string
	MessageID int64
	// NextAttempt is when a retried reply becomes due; zero means due now.
	NextAttempt time.Time
	Content     SendableContent
	Origin      MessageOrigin
	Routing     RoutingMethod
}

// OutboxStore is the durable per-bot delivery queue. NextPending returns
// the oldest pending replies in stored order, due or scheduled: the head
// blocks the ones behind it (FIFO, ADR 0011), and a caller waits until
// NextAttempt instead of sending past it. A reply ends as sent or failed,
// a retry schedules the next attempt without changing its pending state,
// and FailPending ends every queued reply of one bot at once — unlinking
// does that so its replies never leave from the account that replaces it
// (ADR 0040). Depth counts everything still waiting, scheduled or not
// (ADR 0011).
type OutboxStore interface {
	NextPending(
		ctx context.Context, scope, bot string, limit int,
	) ([]OutboxEntry, error)
	MarkSent(ctx context.Context, id int64, providerID string, at time.Time) error
	MarkRetry(
		ctx context.Context, id int64, reason string, nextAttempt, at time.Time,
	) error
	MarkFailed(ctx context.Context, id int64, reason string, at time.Time) error
	FailPending(
		ctx context.Context, scope, bot, reason string, at time.Time,
	) (int, error)
	Depth(ctx context.Context, scope, bot string) (int, error)
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
// answers ErrBusy while a session goroutine for that bot is unfinished,
// CurrentPair answers ErrNotFound when there is none, CancelPair is
// idempotent once a session is final, and EndPair waits for any published
// session's goroutine even after its state is final, so an unpair never races
// its writes (ADR 0021).
type PairingRunner interface {
	StartPair(
		ctx context.Context, scope, bot string, req PairRequest,
	) (*PairingSession, error)
	CurrentPair(ctx context.Context, scope, bot string) (*PairingSession, error)
	CancelPair(ctx context.Context, scope, bot string) error
	EndPair(ctx context.Context, scope, bot string) error
}
