package botflota

import (
	"log/slog"
	"time"
)

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
	Phone       string
	Chunks      int
	Chats       int
	Messages    int
	Duplicate   int
	Skipped     int
	AddressBook int
	Elapsed     time.Duration
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

// LogValue logs the session without its payload: the code is the credential
// that links the account, so it may reach the response that asks for it and
// the QR on screen, never a log line (ADR 0025).
func (s PairingSession) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("scope", s.Scope),
		slog.String("bot", s.Bot),
		slog.String("method", s.Method),
		slog.String("state", string(s.State)),
		slog.Bool("code_present", s.Code != ""),
		slog.Time("started_at", s.StartedAt),
		slog.Time("updated_at", s.UpdatedAt),
		slog.Time("expires_at", s.ExpiresAt),
		slog.String("error", s.Error),
		slog.Any("result", s.Result),
	)
}
