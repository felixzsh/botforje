package botflota

import (
	"errors"
	"time"
)

// SendFailure is the adapter-produced classification of one send attempt
// (ADR 0052). Shared delivery code switches on it with errors.As and never
// parses provider error text.
type SendFailure struct {
	// Permanent marks invalid or unsupported input: the entry fails
	// immediately instead of consuming the retry budget.
	Permanent bool
	// Uncertain marks a timeout after the provider may have accepted the
	// message: it is not a proven rejection. It retries at-least-once and
	// its terminal record says acceptance was not confirmed (ADR 0052).
	Uncertain bool
	// AuthLost marks a lost binding authorization: the entry stays pending
	// and visible while its bot stops, so no later account sends it.
	AuthLost bool
	// RetryAfter is the transient retry hint the provider asked for; zero
	// means the worker's bounded backoff applies.
	RetryAfter time.Duration
	Err        error
}

func (e *SendFailure) Error() string {
	if e == nil || e.Err == nil {
		return "send failed"
	}
	return e.Err.Error()
}

// Unwrap exposes the cause, so errors.Is/As still match what the adapter
// wrapped.
func (e *SendFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// PermanentSendFailure classifies invalid or unsupported input: fail now,
// never retry.
func PermanentSendFailure(err error) *SendFailure {
	return &SendFailure{Permanent: true, Err: err}
}

// TransientSendFailure classifies a failure worth retrying within the
// bounded attempts, honoring the optional retry hint when one is given.
func TransientSendFailure(err error, retryAfter time.Duration) *SendFailure {
	return &SendFailure{RetryAfter: retryAfter, Err: err}
}

// UncertainSendFailure classifies a timeout after possible acceptance: it
// retries at-least-once and never reads as a proven rejection.
func UncertainSendFailure(err error) *SendFailure {
	return &SendFailure{Uncertain: true, Err: err}
}

// AuthorizationLost classifies a lost binding authorization: the entry
// stays pending while its bot stops.
func AuthorizationLost(err error) *SendFailure {
	return &SendFailure{AuthLost: true, Err: err}
}

// AsSendFailure reports the adapter-produced classification of err. A plain
// error carries none and reads as transient with the worker's backoff; the
// typed unsupported-content error always reads as permanent.
func AsSendFailure(err error) (*SendFailure, bool) {
	var failure *SendFailure
	if errors.As(err, &failure) {
		return failure, true
	}
	if errors.Is(err, ErrUnsupportedKind) {
		return PermanentSendFailure(err), true
	}
	return nil, false
}
