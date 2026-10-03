package botflota

import (
	"strings"
	"time"
)

// Persisted shapes: one struct per table, mirroring the columns the SQLite
// adapter reads and writes. Keys, constraints, and encoding live with the DDL
// in adapters/sqlite (ADR 0006, ADR 0026); the ports that read and write them
// live in ports.go.

// LinkedAccount is a bot's linked channel identity: the WhatsApp device JID
// (ADR 0006, ADR 0016). Its lifecycle is PairingStatus.
type LinkedAccount struct {
	Scope string
	Bot   string
	JID   string
}

// Phone is the plain phone number the stored identity addresses, without the
// device or server suffix; the addressing itself never leaves the channel
// (ADR 0020).
func (a LinkedAccount) Phone() string {
	number := a.JID
	if at := strings.IndexByte(number, '@'); at >= 0 {
		number = number[:at]
	}
	if colon := strings.IndexByte(number, ':'); colon >= 0 {
		number = number[:colon]
	}
	return number
}

// Contact is one person's durable identity in a scope (ADR 0029). DisplayName
// is a best-effort push name: possibly empty, never an identifier. FirstSeen
// and LastSeen are the first and last live inbound message; both stay zero
// when the identity was first known through the address book or an API send.
type Contact struct {
	ID            int64
	Scope         string
	Phone         string
	DisplayName   string
	NameUpdatedAt time.Time
	FirstSeen     time.Time
	LastSeen      time.Time
	// These properties are derived from the bot-owned address book and chat
	// history; they are never stored on the identity row.
	AddressBookImported bool
	HasChat             bool
	HasHistory          bool
}

// AddressBookEntry is an address-book entry of a bot's linked account
// (ADR 0037), keyed by (scope, bot, phone) because the book belongs to the
// account, not the scope. It is an identity the account knows, never a Chat.
type AddressBookEntry struct {
	Scope        string
	Bot          string
	Phone        string
	Name         string
	PushName     string
	BusinessName string
	UpdatedAt    time.Time
}

// DisplayName is the name a reader shows; best effort, never an identifier.
func (a AddressBookEntry) DisplayName() string {
	switch {
	case a.Name != "":
		return a.Name
	case a.BusinessName != "":
		return a.BusinessName
	default:
		return a.PushName
	}
}

// Chat is the durable thread between one bot and one contact; it never ends
// (ADR 0026). MessageCount is the live inbound count, not the stored history;
// Guards is the per-action guard state (ADR 0027).
type Chat struct {
	ID           int64
	Scope        string
	Bot          string
	ContactID    int64
	StartedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int64
	Guards       map[string]GuardState
}

type Direction string

const (
	Inbound  Direction = "in"
	Outbound Direction = "out"
)

// Ingest is how a stored message entered Botflota. Unknown covers rows from
// before the distinction existed.
type Ingest string

const (
	IngestLive    Ingest = "live"
	IngestSync    Ingest = "sync"
	IngestAPI     Ingest = "api"
	IngestUnknown Ingest = "unknown"
)

// MessageOrigin is who produced an outbound message: empty for an inbound
// one, unknown and never guessed for an imported one (ADR 0026).
type MessageOrigin string

const (
	OriginPreset   MessageOrigin = "preset"
	OriginLLM      MessageOrigin = "llm"
	OriginOperator MessageOrigin = "operator"
	// OriginAPI is a message the API asked a bot to deliver outside a turn
	// (ADR 0044); it is not a human reply.
	OriginAPI     MessageOrigin = "api"
	OriginUnknown MessageOrigin = "unknown"
)

// RoutingMethod is how the engine chose the transition that handled an inbound
// message (ADR 0010, ADR 0019). Unknown covers an imported row or a run where
// no edge applied.
type RoutingMethod string

const (
	RoutingUnknown         RoutingMethod = "unknown"
	RoutingRoot            RoutingMethod = "root"
	RoutingTriggerExact    RoutingMethod = "trigger_exact"
	RoutingTriggerSequence RoutingMethod = "trigger_sequence"
	RoutingTriggerWords    RoutingMethod = "trigger_words"
	RoutingTriggerFuzzy    RoutingMethod = "trigger_fuzzy"
	RoutingSystemOne       RoutingMethod = "system_one"
	RoutingDefault         RoutingMethod = "default"
	RoutingFallback        RoutingMethod = "fallback"
)

// Message is one message kept for history (ADR 0026). The store derives the
// kind and visible text columns from Content (ADR 0036). SessionID is zero
// when no session run handled it, and ProviderMessageID is the dedup key of
// the history import.
type Message struct {
	ID                int64
	ChatID            int64
	SessionID         int64
	Direction         Direction
	Content           MessageContent
	At                time.Time
	ProviderMessageID string
	Ingest            Ingest
	Origin            MessageOrigin
	Routing           RoutingMethod
}

// HistoryCounts is what a history operation removed or moved: one bot's chats
// and their cascaded sessions, messages, and queued replies, plus its address
// book. A caller reports them, and a limit set releases the storage they held
// (ADR 0042).
type HistoryCounts struct {
	Chats       int64
	Sessions    int64
	Messages    int64
	AddressBook int64
}

// HistoryChat is one chat as an export reads it, with its contact's identity
// beside it so the exported file stands alone (ADR 0042).
type HistoryChat struct {
	Chat
	ContactPhone string
	ContactName  string
}

// ChatControlMode is the effective automation of one chat (ADR 0043). It is
// live runtime state, never a stored column.
type ChatControlMode string

const (
	ChatAuto   ChatControlMode = "auto"
	ChatPaused ChatControlMode = "paused"
	ChatHuman  ChatControlMode = "human"
)

// ChatControl reports one chat's live control state for the read API: the
// effective mode, and the automatic pause deadline while it is counting down.
type ChatControl struct {
	Mode       ChatControlMode
	PausedTill time.Time
}

// ChatSummary is a chat joined with its contact identity, for the read API.
// Messages, Sync, and Live count the stored rows by ingest kind; the embedded
// MessageCount stays the live inbound count (ADR 0035).
type ChatSummary struct {
	Chat
	Phone       string
	DisplayName string
	Messages    int64
	Sync        int64
	Live        int64
}

// Session is one bounded run of the graph for a chat; a chat has many over
// time (ADR 0026).
type Session struct {
	ID           int64
	ChatID       int64
	Node         string
	VisitedNodes []string
	GraphContext map[string]any
	NodeContext  map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

// Expired reports whether the session is over at t; a zero expiry never
// expires.
func (s *Session) Expired(t time.Time) bool {
	return !s.ExpiresAt.IsZero() && !t.Before(s.ExpiresAt)
}

// GuardState is one action's guard state, stored in the chat keyed by action
// name (ADR 0027).
type GuardState struct {
	Cooldown  *CooldownState  `json:"cooldown,omitempty"`
	RateLimit *RateLimitState `json:"rate_limit,omitempty"`
}

type CooldownState struct {
	LastRun time.Time `json:"last_run"`
}

type RateLimitState struct {
	Count       int       `json:"count"`
	WindowStart time.Time `json:"window_start"`
}

type PairingState string

const (
	PairingPaired    PairingState = "paired"
	PairingNotPaired PairingState = "not paired"
	PairingUnpaired  PairingState = "unpaired"
)

// PairingStatus records the last confirmed transition of a bot's pairing.
type PairingStatus struct {
	Scope      string
	Bot        string
	State      PairingState
	LastChange time.Time
}

// OwnerToken is the instance owner's credential (ADR 0025). Only the hash is
// stored; the clear token is written once to a 0600 file under data_dir and
// never kept here.
type OwnerToken struct {
	Hash      []byte
	CreatedAt time.Time
}

// AccessToken is a scoped bearer credential for the API (ROADMAP Phase 13),
// hash only. An empty ExpiresAt never expires; an empty RevokedAt is active.
type AccessToken struct {
	ID        int64
	Name      string
	Hash      []byte
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt time.Time
}

func (t *AccessToken) Active(at time.Time) bool {
	if !t.RevokedAt.IsZero() {
		return false
	}
	return t.ExpiresAt.IsZero() || at.Before(t.ExpiresAt)
}
