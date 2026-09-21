package botflota

import (
	"time"
)

// Persisted shapes: one struct per table. Their fields mirror the columns the
// SQLite adapter reads and writes; keys, constraints, and encoding (JSON,
// RFC 3339 times) live with the DDL in adapters/sqlite (ADR 0006, ADR 0026).
// The store ports that read and write them live in ports.go.

// LinkedAccount is a bot's linked channel identity, the whatsmeow device JID.
// It is keyed by (scope, bot) like every entity (ADR 0016). Pairing is the
// lifecycle of that link and lives in PairingStatus.
type LinkedAccount struct {
	Scope string
	Bot   string
	JID   string
}

// Sender is the durable profile of one person who writes to bots in a scope,
// keyed by (scope, phone). A person may write to several bots of the same
// scope; the bot-person relationship is a Chat (ADR 0029). DisplayName is the
// WhatsApp push name: best effort, possibly empty, and never an identifier.
type Sender struct {
	ID            int64
	Scope         string
	Phone         string
	DisplayName   string
	NameUpdatedAt time.Time
	FirstSeen     time.Time
	LastSeen      time.Time
}

// Contact is an address-book entry of one bot's linked account, copied from
// the channel's contact data when the bot imports contacts. It is keyed by
// (scope, bot, phone), because the address book belongs to the linked account,
// not to the scope: two bots of one scope may link accounts with different
// books. It is not a Sender: a contact is an identity the account knows,
// whether or not it ever wrote (ADR 0029, ADR 0037).
type Contact struct {
	Scope        string
	Bot          string
	Phone        string
	Name         string
	PushName     string
	BusinessName string
	UpdatedAt    time.Time
}

// DisplayName is the name a reader shows: the address-book name first, then
// the verified business name, then the push name. It is best effort and never
// an identifier; the phone number is the key.
func (c Contact) DisplayName() string {
	switch {
	case c.Name != "":
		return c.Name
	case c.BusinessName != "":
		return c.BusinessName
	default:
		return c.PushName
	}
}

// Chat is the durable thread between one bot and one sender, one per
// (scope, bot, sender). It never ends and gathers every message (ADR 0026).
// MessageCount is the inbound count for this bot until message history is
// recorded; Guards is the per-action guard state (ADR 0027, ADR 0029).
type Chat struct {
	ID           int64
	Scope        string
	Bot          string
	SenderID     int64
	StartedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int64
	Guards       map[string]GuardState
}

// Direction is the side of a stored message.
type Direction string

const (
	Inbound  Direction = "in"
	Outbound Direction = "out"
)

// Ingest is how a stored message entered Botflota: live traffic, a WhatsApp
// history sync, or an API operation. Unknown covers rows from before the
// distinction existed.
type Ingest string

const (
	IngestLive    Ingest = "live"
	IngestSync    Ingest = "sync"
	IngestAPI     Ingest = "api"
	IngestUnknown Ingest = "unknown"
)

// MessageOrigin is who produced an outbound message. An inbound message leaves
// it empty: its direction already says it came from the sender. An imported
// outbound message is unknown, never guessed (ADR 0026).
type MessageOrigin string

const (
	OriginPreset   MessageOrigin = "preset"
	OriginLLM      MessageOrigin = "llm"
	OriginOperator MessageOrigin = "operator"
	OriginUnknown  MessageOrigin = "unknown"
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

// Message is one message kept for history. Content is the classified payload;
// the store derives its kind and visible text for the columns it searches
// (ADR 0026, ADR 0036). SessionID is zero when no session run handled it, like
// an imported message. ProviderMessageID is the channel's own id and the dedup
// key of the history import; a channel that does not report one leaves it
// empty.
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

// ChatSummary is a chat joined with its sender profile, for the read API
// (ADR 0026, ADR 0029). Messages, Sync, and Live count the stored rows by
// ingest kind, so a reader sees the imported backlog too (ADR 0035); the
// embedded MessageCount stays the live inbound count of ADR 0029.
type ChatSummary struct {
	Chat
	Phone       string
	DisplayName string
	Messages    int64
	Sync        int64
	Live        int64
}

// Session is one bounded run of the graph for a chat; a chat has many over
// time (ADR 0026). It is the persisted state the engine resumes.
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

// Expired reports whether the session is over at t.
func (s *Session) Expired(t time.Time) bool {
	return !s.ExpiresAt.IsZero() && !t.Before(s.ExpiresAt)
}

// GuardState is one action's accumulated guard state, one field per guard type
// (ADR 0027). It is stored in the chat, keyed by action name.
type GuardState struct {
	Cooldown  *CooldownState  `json:"cooldown,omitempty"`
	RateLimit *RateLimitState `json:"rate_limit,omitempty"`
}

// CooldownState is when a cooldown-guarded action last ran.
type CooldownState struct {
	LastRun time.Time `json:"last_run"`
}

// RateLimitState is the fixed-window counter of one rate-limited action.
type RateLimitState struct {
	Count       int       `json:"count"`
	WindowStart time.Time `json:"window_start"`
}

// PairingState is the persisted local state of one bot's channel pairing.
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

// AccessToken is a scoped bearer credential for the API (ROADMAP Phase 13).
// Only the hash is stored; the clear token is shown once when it is minted. An
// empty ExpiresAt never expires, an empty RevokedAt is active.
type AccessToken struct {
	ID        int64
	Name      string
	Hash      []byte
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt time.Time
}

// Active reports whether the token may still authenticate at at.
func (t *AccessToken) Active(at time.Time) bool {
	if !t.RevokedAt.IsZero() {
		return false
	}
	return t.ExpiresAt.IsZero() || at.Before(t.ExpiresAt)
}
