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

// IncomingMessage is one inbound message. Sender is the resolved phone number
// in plain digits (e.g. "5215500000000"); the JID and any LID stay inside the
// channel adapter (ADR 0020).
type IncomingMessage struct {
	Bot     string
	Sender  string
	Body    string
	ID      string
	IsGroup bool
	At      time.Time
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
// Recipient is the plain phone number the reply is for; the adapter maps it to
// the transport address (ADR 0020).
type OutgoingMessage struct {
	Bot       string
	Recipient string
	Kind      OutgoingKind
	Body      string
	Location  *Location
}
