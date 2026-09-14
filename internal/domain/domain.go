// Package domain holds the runtime types and ports shared by the engine and
// its adapters. It never imports YAML, SQL, whatsmeow, or LLM SDK types
// (ADR 0004).
package domain

import (
	"errors"
	"time"
)

// ErrNotFound reports a persisted record that does not exist.
var ErrNotFound = errors.New("not found")

// ErrNoLLM reports an LLM-only operation with no provider configured.
var ErrNoLLM = errors.New("no LLM configured")

// OutgoingKind classifies an outbound message.
type OutgoingKind string

const (
	KindText     OutgoingKind = "text"
	KindLocation OutgoingKind = "location"
)

// Direction is the side of a stored message.
type Direction string

const (
	Inbound  Direction = "in"
	Outbound Direction = "out"
)

// IncomingMessage is one inbound message with its sender resolved by the
// channel adapter (ADR 0013).
type IncomingMessage struct {
	Bot       string
	Sender    string // resolved PN; a LID while the session is unresolved
	SenderLID string
	ReplyTo   string // JID the message arrived from; empty falls back to Sender
	Body      string
	ID        string
	At        time.Time
}

// Location is a map pin sent as its own message.
type Location struct {
	Latitude    float64
	Longitude   float64
	Name        string
	Address     string
	URL         string
	Description string
}

// OutgoingMessage is one message the engine asks the channel to deliver.
type OutgoingMessage struct {
	Bot      string
	To       string // JID the message arrived from; never fabricated (ADR 0013)
	Body     string
	Kind     OutgoingKind
	Location *Location
}

// Session is the persisted per-(bot, sender) conversation state (ADR 0010).
type Session struct {
	Bot          string
	Sender       string
	SenderLID    string
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
