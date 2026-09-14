package domain

import (
	"context"
	"time"
)

// SessionStore persists conversation state. The domain owns the interface; an
// adapter implements it (ADR 0006).
type SessionStore interface {
	Get(ctx context.Context, bot, sender string) (*Session, error)
	Save(ctx context.Context, session *Session) error
	Delete(ctx context.Context, bot, sender string) error
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

// MessageStore records message history.
type MessageStore interface {
	Append(ctx context.Context, message *Message) error
	List(ctx context.Context, conversationID int64, limit int) ([]Message, error)
}
