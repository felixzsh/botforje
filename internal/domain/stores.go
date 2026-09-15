package domain

import (
	"context"
	"time"
)

// Persisted shapes: one struct per table. Their fields mirror the columns the
// SQLite adapter reads and writes; keys, constraints, and encoding (JSON,
// RFC 3339 times) live with the DDL in internal/sqlite (ADR 0006, ADR 0026).

// Account is a bot's paired channel identity, the whatsmeow device JID. It is
// keyed by (scope, bot) like every entity (ADR 0016).
type Account struct {
	Scope string
	Bot   string
	JID   string
}

// Chat is the durable thread between one bot and one sender, one per
// (scope, bot, sender). It never ends and gathers every message (ADR 0026).
// Guards is the per-action guard state (ADR 0027).
type Chat struct {
	ID        int64
	Scope     string
	Bot       string
	Sender    string
	StartedAt time.Time
	UpdatedAt time.Time
	Guards    map[string]GuardState
}

// Direction is the side of a stored message.
type Direction string

const (
	Inbound  Direction = "in"
	Outbound Direction = "out"
)

// Message is one message kept for history. Kind is the shared OutgoingKind,
// declared with the runtime message types. SessionID is zero when no session
// run handled it.
type Message struct {
	ID        int64
	ChatID    int64
	SessionID int64
	Direction Direction
	Kind      OutgoingKind
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

// Store ports: the domain owns the interfaces; an adapter implements them
// (ADR 0006).

// ChatStore records the durable chat with each person. Ensure is a
// get-or-create; SaveGuards persists the chat's per-action guard state
// (ADR 0026, ADR 0027).
type ChatStore interface {
	Ensure(ctx context.Context, scope, bot, sender string, at time.Time) (*Chat, error)
	Touch(ctx context.Context, id int64, at time.Time) error
	SaveGuards(ctx context.Context, chatID int64, guards map[string]GuardState) error
}

// SessionStore records a chat's session runs. Latest returns the most recent
// one, or domain.ErrNotFound when the chat never had a session (ADR 0026).
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

// AccountStore records paired channel accounts and their pairing status
// (ADR 0006).
type AccountStore interface {
	Get(ctx context.Context, scope, bot string) (*Account, error)
	List(ctx context.Context) ([]Account, error)
	Put(ctx context.Context, account *Account) error
	Delete(ctx context.Context, scope, bot string) error
	GetPairingStatus(ctx context.Context, scope, bot string) (*PairingStatus, error)
	SetPairingStatus(ctx context.Context, status *PairingStatus) error
}
