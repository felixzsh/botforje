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
type Chat struct {
	ID        int64
	Scope     string
	Bot       string
	Sender    string
	StartedAt time.Time
	UpdatedAt time.Time
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
	Cooldowns    map[string]time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

// Expired reports whether the session is over at t.
func (s *Session) Expired(t time.Time) bool {
	return !s.ExpiresAt.IsZero() && !t.Before(s.ExpiresAt)
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
// get-or-create (ADR 0026).
type ChatStore interface {
	Ensure(ctx context.Context, scope, bot, sender string, at time.Time) (*Chat, error)
	Touch(ctx context.Context, id int64, at time.Time) error
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
