package domain

import (
	"context"
	"time"
)

// SessionStore persists conversation state. The domain owns the interface; an
// adapter implements it (ADR 0006). Sessions are keyed by (scope, bot, sender)
// so two scopes never share conversation state (ADR 0005).
type SessionStore interface {
	Get(ctx context.Context, scope, bot, sender string) (*Session, error)
	Save(ctx context.Context, session *Session) error
	Delete(ctx context.Context, scope, bot, sender string) error
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

// Message is one message kept for history.
type Message struct {
	ID             int64
	ConversationID int64
	Direction      Direction
	Kind           OutgoingKind
	Body           string
	At             time.Time
}

// ConversationStore records conversation threads.
type ConversationStore interface {
	Start(ctx context.Context, conversation *Conversation) error
	Touch(ctx context.Context, id int64, at time.Time) error
}

// Account is a bot's paired channel identity, the whatsmeow device JID. It is
// keyed by (scope, bot) like every entity (ADR 0016).
type Account struct {
	Scope string
	Bot   string
	JID   string
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

// MessageStore records message history.
type MessageStore interface {
	Append(ctx context.Context, message *Message) error
	List(ctx context.Context, conversationID int64, limit int) ([]Message, error)
}
