package botflota

import "time"

// PairState is one live pairing session's lifecycle (ADR 0025).
type PairState string

const (
	PairPending   PairState = "pending"
	PairWaiting   PairState = "waiting"
	PairSucceeded PairState = "paired"
	PairFailed    PairState = "failed"
	PairCancelled PairState = "cancelled"
	PairExpired   PairState = "expired"
)

type PairRequest struct {
	// FullHistory asks for the full history window at registration (ADR 0035).
	FullHistory bool
}

// PairingResult is what a finished pairing stored, for whoever asked for the
// session instead of a log (ADR 0038); only the phone travels, never the JID
// (ADR 0020).
type PairingResult struct {
	Phone     string
	Chats     int
	Messages  int
	Duplicate int
	Skipped   int
	Contacts  int
	Elapsed   time.Duration
}

// PairingSession is one bot's live pairing session, read until Final
// (ADR 0025).
type PairingSession struct {
	Scope     string
	Bot       string
	Method    string // how the phone links: "qr" today
	State     PairState
	Code      string // the latest QR code to scan
	StartedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
	Error     string
	Result    PairingResult
}

func (s PairingSession) Final() bool {
	switch s.State {
	case PairSucceeded, PairFailed, PairCancelled, PairExpired:
		return true
	}
	return false
}
