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

// Message is one message kept for history. Kind is the shared MessageKind,
// declared with the runtime message types. SessionID is zero when no session
// run handled it.
type Message struct {
	ID        int64
	ChatID    int64
	SessionID int64
	Direction Direction
	Kind      MessageKind
	Body      string
	At        time.Time
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
