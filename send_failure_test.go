package botflota

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestAsSendFailureClassifiesWithoutParsing: shared delivery code switches on
// the adapter-produced outcome with errors.As, never on provider error text.
// A plain error reads as transient with the worker's backoff (ADR 0052).
func TestAsSendFailureClassifiesWithoutParsing(t *testing.T) {
	permanent := PermanentSendFailure(errors.New("bad input"))
	transient := TransientSendFailure(errors.New("busy"), 45*time.Second)
	uncertain := UncertainSendFailure(errors.New("timeout after send"))
	authLost := AuthorizationLost(errors.New("revoked"))
	unsupported := fmt.Errorf("mail: %w", ErrUnsupportedKind)
	tests := []struct {
		name           string
		err            error
		wantPermanent  bool
		wantUncertain  bool
		wantAuthLost   bool
		wantRetryAfter time.Duration
		wantOK         bool
	}{
		{
			name:          "unsupported content is permanent",
			err:           unsupported,
			wantPermanent: true,
			wantOK:        true,
		},
		{
			name:          "permanent input fails at once",
			err:           permanent,
			wantPermanent: true,
			wantOK:        true,
		},
		{
			name:           "transient honors the retry hint",
			err:            transient,
			wantRetryAfter: 45 * time.Second,
			wantOK:         true,
		},
		{
			name:          "uncertain is not permanent",
			err:           uncertain,
			wantUncertain: true,
			wantOK:        true,
		},
		{
			name:         "authorization loss is its own class",
			err:          authLost,
			wantAuthLost: true,
			wantOK:       true,
		},
		{
			name:   "plain errors carry no classification",
			err:    errors.New("boom"),
			wantOK: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure, ok := AsSendFailure(test.err)
			if ok != test.wantOK {
				t.Fatalf("AsSendFailure ok = %v, want %v", ok, test.wantOK)
			}
			if !ok {
				return
			}
			if failure.Permanent != test.wantPermanent ||
				failure.Uncertain != test.wantUncertain ||
				failure.AuthLost != test.wantAuthLost ||
				failure.RetryAfter != test.wantRetryAfter {
				t.Fatalf("failure = %#v, want the constructed outcome", failure)
			}
			if failure.Error() == "" {
				t.Fatal("the classification lost its cause")
			}
		})
	}
}

// TestUnsupportedKindClassifiesPermanent: today's only typed class reads as
// permanent without any adapter wrapping (ADR 0036, ADR 0052).
func TestUnsupportedKindClassifiesPermanent(t *testing.T) {
	err := errors.New("channel cannot send it: " + ErrUnsupportedKind.Error())
	joined := errors.Join(err, ErrUnsupportedKind)
	failure, ok := AsSendFailure(joined)
	if !ok || !failure.Permanent {
		t.Fatalf("AsSendFailure = %#v, %v, want permanent", failure, ok)
	}
}
