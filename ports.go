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
	HasCredentials(ctx context.Context, account *Binding) (bool, error)
}

// AccountUnpairer removes a linked account and its local credentials, the
// destructive counterpart of DevicePresence (ADR 0021, ADR 0039). The caller
// stops the running bot first, so no channel holds the connection while its
// credentials go away.
type AccountUnpairer interface {
	UnpairAccount(ctx context.Context, scope, bot string) error
}

// OutboundObserver is an optional MessageChannel capability: a channel that
// reports messages leaving the linked account outside Botflota, so a bot can
// hand the chat to a human (ADR 0043). A channel that cannot tell its own echo
// apart from an operator reply does not implement it.
type OutboundObserver interface {
	Outbound() <-chan ExternalSend
}

// HistorySyncer is an optional MessageChannel capability: asking the primary
// device for older messages of one conversation over the live connection
// (ADR 0035).
type HistorySyncer interface {
	SyncHistory(
		ctx context.Context, target ChatTarget, count int, timeout time.Duration,
	) (HistoryResult, error)
}

// HistoryResult reports one chat's on-demand history backfill (ADR 0035).
// Received counts the provider entries the channel answered with, Imported what
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

// ChatStore records the durable chat with each conversation (ADR 0026,
// ADR 0027, ADR 0029, ADR 0035). Ensure is a get-or-create keyed by the
// complete conversation target; a nonzero peerID supplies explicit known
// participant evidence and must resolve under the same scope and provider.
type ChatStore interface {
	Ensure(
		ctx context.Context, scope, bot string, target ChatTarget, peerID int64,
		at time.Time,
	) (*Chat, error)
	ForTarget(ctx context.Context, scope, bot string, target ChatTarget) (*Chat, error)
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

// PeerStore records the identity of each person a scope knows.
// EnsurePeer creates an identity without a first inbound message, so the
// directory and an API send never consume the first-contact hook.
// RecordInbound records a live inbound: it claims first_seen conditionally —
// a row actually updated is the answer, so inbounds that share one timestamp
// still report first only once — and commits the claim with the activity and
// the name, reporting the claim as the hook (ADR 0029). SetDisplayName fills
// an unknown name from channel contact data without touching first or last
// seen (ADR 0035). List is the
// scope-level read: identities most recently seen first, filtered by
// first_seen when since is not zero, with an opaque cursor a caller passes
// back unchanged (ADR 0044).
type PeerStore interface {
	EnsurePeer(ctx context.Context, scope string, key IdentityKey, at time.Time) (*Peer, error)
	RecordInbound(
		ctx context.Context, scope string, key IdentityKey, name string, at time.Time,
	) (*Peer, bool, error)
	Get(ctx context.Context, scope string, key IdentityKey) (*Peer, error)
	SetDisplayName(ctx context.Context, scope string, key IdentityKey, name string, at time.Time) error
	List(
		ctx context.Context, scope string, since time.Time, limit int, cursor string,
	) (PeerPage, error)
}

// PeerPage is one page of a scope's peer identities. NextCursor is empty
// on the last page; a caller sends it back through List to read the next one.
type PeerPage struct {
	Peers      []Peer
	NextCursor string
}

// EventPeerFirstSeen is the first-contact event: a peer wrote to a bot of the
// scope for the first time (ADR 0045).
const EventPeerFirstSeen = "peer.first_seen"

// PeerFirstSeen is one peer's first allowed inbound message in a scope,
// carrying the bot that saw it and when (ADR 0029, ADR 0045).
type PeerFirstSeen struct {
	Scope string
	Bot   string
	Peer  Peer
	At    time.Time
}

// PeerFirstSeenNotifier receives the first-contact hook so an adapter can
// deliver it outside the engine (ADR 0045). The engine calls it once per new
// profile and the call records the event; delivery belongs to the adapter.
type PeerFirstSeenNotifier interface {
	PeerFirstSeen(ctx context.Context, event PeerFirstSeen) error
}

// HistoryStore owns the operations that span one bot's whole history — its
// chats, sessions, messages, and directory — and must be atomic across all of
// them. DeleteBot removes that history and leaves the scope's peer identities
// alone (ADR 0029); MoveBot moves it to another bot of the same scope and
// refuses a destination that already holds a chat, because history is moved
// and never merged (ADR 0042).
type HistoryStore interface {
	HistoryReader
	DeleteBot(ctx context.Context, scope, bot string) (HistoryCounts, error)
	MoveBot(
		ctx context.Context, scope, from, to string, at time.Time,
	) (HistoryCounts, error)
}

// HistoryReader streams everything one bot's history holds, so it can leave the
// instance without buffering a whole conversation (ADR 0042). The sink is called
// once per stored row, table by table, in stored order; an error from any call
// stops the read and is returned. Peer identities are not history — they
// belong to the scope (ADR 0029) — but a chat carries its peer key and
// name so the result stands on its own.
type HistoryReader interface {
	ReadHistory(ctx context.Context, scope, bot string, sink HistorySink) error
}

// HistoryTable names one table of a bot's history, so a sink knows which file
// or destination the rows that follow belong to (ADR 0042).
type HistoryTable string

const (
	TableChats     HistoryTable = "chats"
	TableSessions  HistoryTable = "sessions"
	TableMessages  HistoryTable = "messages"
	TableDirectory HistoryTable = "directory"
)

// HistorySink receives one bot's history as it is read. BeginTable is called
// once before a table's rows, so a sink that writes one file per table can
// finish the previous file before the next one starts; the rows that follow
// belong to that table, in stored order.
type HistorySink interface {
	BeginTable(table HistoryTable) error
	Chat(HistoryChat) error
	Session(Session) error
	Message(Message) error
	DirectoryEntry(DirectoryEntry) error
}

// DirectoryStore records the account-directory entries of each bot's linked
// account when contact import is on (ADR 0037, ADR 0051). Put creates a proven
// directory entry and merges non-empty names; Merge updates an existing entry
// without creating membership based on a message. List returns the most
// recently updated entries first; zero limit means all entries.
type DirectoryStore interface {
	Put(ctx context.Context, entry *DirectoryEntry) error
	Merge(ctx context.Context, entry *DirectoryEntry) error
	List(ctx context.Context, scope, bot string, limit int) ([]DirectoryEntry, error)
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
// ErrDuplicate when the message's destination already holds the same provider
// id; the outbox records the channel id of a delivered reply when it marks it
// sent. Oldest returns the oldest message that can anchor a history request —
// one with a channel id, never a call row (ADR 0035). History returns up to
// limit messages oldest first: the last ones when afterID is zero, or those
// newer than afterID. HasProviderID is the cheap read the engine uses before it
// runs a turn, so a redelivery never repeats an action.
type MessageStore interface {
	Append(ctx context.Context, message *Message) error
	Oldest(ctx context.Context, chatID int64) (*Message, error)
	History(ctx context.Context, chatID, afterID int64, limit int) ([]Message, error)
	HasProviderID(ctx context.Context, target ChatTarget, providerID string) (bool, error)
}

// SendRecord is one message the API asks a bot to deliver outside a turn: no
// session run, no inbound message (ADR 0044). Target is the required exact
// destination; Recipient is the optional known recipient identity, nil when
// the caller knows only the destination. The store creates the peer identity
// and the chat when they are present or missing, so a notice can reach a
// destination that never wrote first.
type SendRecord struct {
	Scope     string
	Bot       string
	Target    ChatTarget
	Recipient *IdentityKey
	Content   SendableContent
	// ClientID is the caller's idempotency key; empty means no key.
	ClientID string
	At       time.Time
}

// SendResult is what CommitSend stored.
type SendResult struct {
	MessageID int64
	OutboxID  int64
}

// SendStore commits one API-origin outbound message as one unit: the known
// recipient identity, the chat, the stored message, the chat's activity, and
// the queued delivery either all land or none does. A ClientID the bot already
// accepted reports ErrDuplicate with the accepted message id in the result, so
// a caller that lost the response can reconcile (ADR 0044).
type SendStore interface {
	CommitSend(ctx context.Context, send SendRecord) (SendResult, error)
}

// TurnRecord is one message turn's writes: the session it advanced, the
// inbound message, the replies and their destinations, chat activity, and the
// guard state it changed (ADR 0026, ADR 0027). Scope, Bot, Session, and
// Inbound are required; CommitTurn links the messages to the session it saved,
// reports their stored ids back through them, and queues every reply for
// delivery in the same unit, so a crash never leaves a stored reply nobody
// sends.
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

// OutboundRecord is one reply of the turn: the stored message and the exact
// conversation it goes to, which may differ from the chat whose history
// records it when a step overrides the destination.
type OutboundRecord struct {
	Message *Message
	Target  ChatTarget
}

// TurnStore commits one message turn as one unit: either the whole turn
// lands — session, messages, reply-recipient identities, chat activity,
// guards, and queued replies — or none of it does. A partial turn would leave
// a session advanced with lost replies, which is why the engine never writes
// those shapes directly (ADR 0026, ADR 0027, ADR 0011).
type TurnStore interface {
	CommitTurn(ctx context.Context, turn TurnRecord) error
	// Record stores one message that is not part of a routable turn and queues
	// nothing, moving its chat's activity in the same transaction: an inbound
	// the bot suppressed while a human held the chat, or an outbound the
	// operator sent outside Botflota (ADR 0043). The message carries its
	// ChatID and no session run; Append reports ErrDuplicate on a redelivery.
	Record(ctx context.Context, message *Message, at time.Time) error
}

// OutboxEntry is one queued reply: the stored message id, the exact
// destination, and the sendable content, read back from the queue (ADR 0011).
type OutboxEntry struct {
	ID        int64
	Attempts  int
	Target    ChatTarget
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
	// FailPendingForChat ends the queued replies stored under one chat, so a
	// chat handed to a human never sends an automatic reply already queued
	// (ADR 0043). It leaves the bot's other chats and its messages alone.
	FailPendingForChat(
		ctx context.Context, chatID int64, reason string, at time.Time,
	) (int, error)
	// IsPending reports whether one queued reply is still waiting; the worker
	// rechecks it right before sending, so a reply cancelled for a hand-over
	// after it was read is not sent (ADR 0043).
	IsPending(ctx context.Context, id int64) (bool, error)
	Depth(ctx context.Context, scope, bot string) (int, error)
}

// WebhookDelivery is one queued webhook delivery (ADR 0045). The event id
// identifies the fact and is shared by a target's retries; the delivery id is
// the row's own identifier and stays the same across them. Payload is the raw
// JSON body, signed and sent unchanged.
type WebhookDelivery struct {
	ID          int64
	EventID     string
	DeliveryID  string
	URL         string
	Event       string
	Scope       string
	Payload     []byte
	Attempts    int
	NextAttempt time.Time
}

// WebhookStore is the durable outbound webhook queue: one row per (event,
// target). Enqueue is idempotent on that pair, NextPending returns the rows
// due at a time, and the outcome of an attempt is recorded, never only logged
// (ADR 0045).
type WebhookStore interface {
	Enqueue(ctx context.Context, delivery *WebhookDelivery) error
	NextPending(ctx context.Context, at time.Time, limit int) ([]WebhookDelivery, error)
	MarkSent(ctx context.Context, id int64, at time.Time) error
	MarkRetry(
		ctx context.Context, id int64, reason string, next, at time.Time,
	) error
	MarkFailed(ctx context.Context, id int64, reason string, at time.Time) error
}

// BindingStore records the channel account linked to each bot
// (ADR 0006, ADR 0051). Pairing is tracked separately by PairingStore.
type BindingStore interface {
	Get(ctx context.Context, scope, bot string) (*Binding, error)
	List(ctx context.Context) ([]Binding, error)
	Put(ctx context.Context, account *Binding) error
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
