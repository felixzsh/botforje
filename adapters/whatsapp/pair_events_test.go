package whatsapp

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
)

// codeEvent is one challenge the provider offers down the pairing channel.
func codeEvent(code string) whatsmeow.QRChannelItem {
	return whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: code}
}

// TestPairEventsWiring pins the channel wiring a code setup depends on: the
// link happens on the first challenge only, a QR setup renders every challenge,
// and a passkey step the companion cannot answer is reported rather than
// resolved (ADR 0028).
func TestPairEventsWiring(t *testing.T) {
	cases := []struct {
		name        string
		link        bool
		events      []whatsmeow.QRChannelItem
		wantLinks   int
		wantRenders int
		wantPending int
		wantResult  bool
	}{
		{
			name: "code links on the first challenge",
			link: true,
			events: []whatsmeow.QRChannelItem{
				codeEvent("ABCD-EFGH"), codeEvent("IJKL-MNOP"),
				whatsmeow.QRChannelSuccess,
			},
			wantLinks:  1,
			wantResult: true,
		},
		{
			name: "qr renders every challenge",
			events: []whatsmeow.QRChannelItem{
				codeEvent("qr-1"), codeEvent("qr-2"),
				whatsmeow.QRChannelSuccess,
			},
			wantRenders: 2,
			wantResult:  true,
		},
		{
			name: "passkey request is reported and not answered",
			events: []whatsmeow.QRChannelItem{
				{Event: whatsmeow.QRChannelEventPasskeyRequest},
				{Event: whatsmeow.QRChannelEventPasskeyResponse},
				whatsmeow.QRChannelSuccess,
			},
			wantPending: 1,
			wantResult:  true,
		},
		{
			// The provider emits this only after a WebAuthn response, which
			// this companion never sends, so a healthy flow cannot reach it.
			// The case pins that a stray one still does not resolve the
			// attempt.
			name:   "stray passkey confirmation does not resolve the attempt",
			events: []whatsmeow.QRChannelItem{{Event: whatsmeow.QRChannelEventPasskeyResponse}},
		},
		{
			name:   "a closed channel asks for a retry",
			events: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan whatsmeow.QRChannelItem, len(tc.events))
			for _, item := range tc.events {
				events <- item
			}
			close(events)
			var links, renders, pendings int
			var link func(context.Context) error
			if tc.link {
				link = func(context.Context) error {
					links++
					return nil
				}
			}
			result, retry, err := pairEvents(
				context.Background(), events,
				func(string) { renders++ },
				link,
				func() { pendings++ },
				func() (PairResult, error) {
					return PairResult{Identity: waKey("5215500000001")}, nil
				},
				time.Minute,
			)
			if err != nil {
				t.Fatalf("pairEvents: %v", err)
			}
			if links != tc.wantLinks || renders != tc.wantRenders ||
				pendings != tc.wantPending {
				t.Fatalf("links/renders/pendings = %d/%d/%d, want %d/%d/%d",
					links, renders, pendings, tc.wantLinks, tc.wantRenders,
					tc.wantPending)
			}
			if retry != !tc.wantResult {
				t.Fatalf("retry = %v, want a resolved attempt = %v", retry,
					tc.wantResult)
			}
			if tc.wantResult && result.Identity.Provider == "" {
				t.Fatalf("result = %#v, want the success outcome", result)
			}
		})
	}
}

// TestPairEventsEndsOnItsOwnDeadline: an attempt that resolves nothing is
// retried by the caller, so a gated account never hangs the worker.
func TestPairEventsEndsOnItsOwnDeadline(t *testing.T) {
	events := make(chan whatsmeow.QRChannelItem)
	_, retry, err := pairEvents(
		context.Background(), events, func(string) {}, nil, nil,
		func() (PairResult, error) { return PairResult{}, nil },
		time.Millisecond,
	)
	if err != nil {
		t.Fatalf("pairEvents: %v", err)
	}
	if !retry {
		t.Fatal("an attempt past its deadline must be retried")
	}
}

// TestPairEventsReportsFailures: a provider error ends the attempt with the
// error, and a scanned QR without multi-device reports its own condition.
func TestPairEventsReportsFailures(t *testing.T) {
	cases := []struct {
		name string
		item whatsmeow.QRChannelItem
	}{
		{
			"pairing error",
			whatsmeow.QRChannelItem{
				Event: whatsmeow.QRChannelEventError,
				Error: context.DeadlineExceeded,
			},
		},
		{
			"without multidevice",
			whatsmeow.QRChannelScannedWithoutMultidevice,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan whatsmeow.QRChannelItem, 1)
			events <- tc.item
			close(events)
			if _, _, err := pairEvents(
				context.Background(), events, func(string) {}, nil, nil,
				func() (PairResult, error) { return PairResult{}, nil },
				time.Minute,
			); err == nil {
				t.Fatal("pairEvents = nil, want the reported failure")
			}
		})
	}
}

// TestPairEventsHonorsCancellation: ending a live session stops its attempt
// without waiting for the deadline.
func TestPairEventsHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan whatsmeow.QRChannelItem)
	cancel()
	_, retry, err := pairEvents(
		ctx, events, func(string) {}, nil, nil,
		func() (PairResult, error) { return PairResult{}, nil },
		time.Minute,
	)
	if retry || err == nil {
		t.Fatalf("pairEvents = retry %v, err %v; want the cancellation", retry, err)
	}
}
