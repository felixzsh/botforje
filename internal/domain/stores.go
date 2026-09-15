package domain

import (
	"context"
	"time"
)

// Persisted shapes: one struct per table. Their fields mirror the columns the
// SQLite adapter reads and writes; keys, constraints, and encoding (JSON,
// RFC 3339 times) live with the DDL in internal/sqlite (ADR 0006).

// Account is a bot's paired channel identity, the whatsmeow device JID. It is
// keyed by (scope, bot) like every entity (ADR 0016).
type Account struct {
	Scope string
	Bot   string
	JID   string
}

// Conversation is a thread between one bot and one sender.
type Conversation struct {
	ID        int64
	Bot       string
	Sender    string
	Scope     string
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
// declared with the runtime message types.
type Message struct {
	ID             int64
	ConversationID int64
	Direction      Direction
	Kind           OutgoingKind
	Body           string
	At             time.Time
}

// Session is the persisted per-(bot, sender) conversation state (ADR 0010).
type Session struct {
	Bot          string
	Sender       string
	Scope        string
	Node         string
	Memory       []string
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

// SessionStore persists conversation state. Sessions are keyed by
// (scope, bot, sender) so two scopes never share conversation state (ADR 0005).
type SessionStore interface {
	Get(ctx context.Context, scope, bot, sender string) (*Session, error)
	Save(ctx context.Context, session *Session) error
	Delete(ctx context.Context, scope, bot, sender string) error
}

// ConversationStore records conversation threads.
type ConversationStore interface {
	Start(ctx context.Context, conversation *Conversation) error
	Touch(ctx context.Context, id int64, at time.Time) error
}

// MessageStore records message history.
type MessageStore interface {
	Append(ctx context.Context, message *Message) error
	List(ctx context.Context, conversationID int64, limit int) ([]Message, error)
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
