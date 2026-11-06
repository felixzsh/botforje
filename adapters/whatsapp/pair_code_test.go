package whatsapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// TestNormalizePairPhone covers the number a code link registers: the
// international digits, never a national form the provider would refuse
// (ADR 0028).
func TestNormalizePairPhone(t *testing.T) {
	cases := []struct {
		name  string
		phone string
		want  string
		ok    bool
	}{
		{"international digits", "5215500000001", "5215500000001", true},
		{"formatted", "+52 155 0000 0001", "5215500000001", true},
		{"national leading zero", "015500000001", "", false},
		{"too short", "12345", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizePairPhone(tc.phone)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("normalizePairPhone = %q, %v; want %q", got, err, tc.want)
				}
				return
			}
			if !errors.Is(err, botflota.ErrInvalidParam) {
				t.Fatalf("normalizePairPhone = %v, want ErrInvalidParam", err)
			}
		})
	}
}

// TestCodeLinkOnlyForACodeSetup: a QR setup links with no phone action, so it
// carries no link at all (ADR 0028).
func TestCodeLinkOnlyForACodeSetup(t *testing.T) {
	link, err := PairOptions{}.codeLink()
	if err != nil || link != nil {
		t.Fatalf("codeLink without a phone = %v, %v; want none", link != nil, err)
	}
	if _, err := (PairOptions{Phone: "015500000001"}).codeLink(); !errors.Is(
		err, botflota.ErrInvalidParam,
	) {
		t.Fatalf("codeLink with a national number = %v, want ErrInvalidParam", err)
	}
}

// TestSetupMethodsOfferBothChallenges: the pairing code is a first-class
// method beside the QR, never a replacement for it (ADR 0028).
func TestSetupMethodsOfferBothChallenges(t *testing.T) {
	manager := testPairManager(t, PairManagerOptions{})
	methods, err := manager.SetupMethods(context.Background(), "dev", "alpha")
	if err != nil {
		t.Fatalf("SetupMethods: %v", err)
	}
	want := []string{SetupMethodQR, SetupMethodCode}
	if len(methods) != len(want) {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
	for i, method := range want {
		if methods[i] != method {
			t.Fatalf("methods = %v, want %v", methods, want)
		}
	}
}

// TestSetupCodeDisplaysTheCodeAndWaitsForTheHandoff: the display step carries
// the eight-digit code and what the owner types, the session reports the wait
// as a state instead of blocking a reader, and the phone — not the companion —
// finishes the link (ADR 0028, ADR 0053).
func TestSetupCodeDisplaysTheCodeAndWaitsForTheHandoff(t *testing.T) {
	given := make(chan PairOptions, 1)
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		given <- opts
		opts.OnCode("ABCD-EFGH")
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	started, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{
			Method: SetupMethodCode, RequestKey: "key-1",
			Params: map[string]string{"phone": "+52 155 0000 0001"},
		})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	if started.Method != SetupMethodCode || started.State != botflota.SetupStarting {
		t.Fatalf("session = %#v, want a starting code session", started)
	}
	opts := <-given
	if opts.Phone != "5215500000001" {
		t.Fatalf("phone = %q, want the international digits", opts.Phone)
	}
	if opts.RenderQR == nil || opts.OnCode == nil {
		t.Fatal("the worker received no way to display the code")
	}

	pending := waitSetup(t, manager, "dev", "alpha", started.ID,
		botflota.SetupAwaitingApproval)
	if pending.Step == nil || pending.Step.Kind != botflota.SetupStepDisplay {
		t.Fatalf("step = %#v, want the code display", pending.Step)
	}
	display := pending.Step.Display
	if display == nil || display.Format != botflota.SetupDisplayCode ||
		display.Payload != "ABCD-EFGH" || display.Hint == "" {
		t.Fatalf("display = %#v, want the code and its instruction", display)
	}
	// The session is readable while the phone works: a read never waits for
	// the code to expire.
	if pending.Final() {
		t.Fatalf("state = %s, want a pending state", pending.State)
	}
	if err := manager.CancelSetup(context.Background(), "dev", "alpha",
		started.ID); err != nil {
		t.Fatalf("CancelSetup: %v", err)
	}
}

// TestSetupCodeReportsPendingThenExpiry: a passkey step the companion cannot
// answer is a reported state, not a silent loop, and the session's own expiry
// is reported as an outcome with no step left (ADR 0028).
func TestSetupCodeReportsPendingThenExpiry(t *testing.T) {
	manager, _ := testSetupManager(t, PairManagerOptions{
		Lifetime: 40 * time.Millisecond,
	})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		opts.OnCode("ABCD-EFGH")
		opts.OnPending("finish the passkey step on the phone")
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	started, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{
			Method: SetupMethodCode, RequestKey: "key-1",
			Params: map[string]string{"phone": "5215500000001"},
		})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	pending := waitSetup(t, manager, "dev", "alpha", started.ID,
		botflota.SetupAwaitingApproval)
	if pending.Step == nil || pending.Step.Kind != botflota.SetupStepApproval ||
		pending.Step.Approval == nil ||
		pending.Step.Approval.Instruction != "finish the passkey step on the phone" {
		t.Fatalf("step = %#v, want the reported pending instruction", pending.Step)
	}
	if pending.Revision < 2 {
		t.Fatalf("revision = %d, want a bump for the reported step", pending.Revision)
	}

	expired := waitSetup(t, manager, "dev", "alpha", started.ID,
		botflota.SetupExpired)
	if expired.ErrorCode != "expired" || expired.Step != nil {
		t.Fatalf("session = %#v, want an expired close with no step", expired)
	}
}

// TestSetupCodeRequestKeyIsScopedToItsPhone: a retried start key returns the
// same session only for the same phone, so a different number is a conflicting
// request instead of a silent replacement (ADR 0053).
func TestSetupCodeRequestKeyIsScopedToItsPhone(t *testing.T) {
	release := make(chan struct{})
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		opts.OnCode("ABCD-EFGH")
		<-ctx.Done()
		<-release
		return PairResult{}, ctx.Err()
	}
	defer close(release)
	ctx := context.Background()
	begin := botflota.SetupBegin{
		Method: SetupMethodCode, RequestKey: "key-1",
		Params: map[string]string{"phone": "5215500000001"},
	}
	first, err := manager.BeginSetup(ctx, "dev", "alpha", begin)
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	again, err := manager.BeginSetup(ctx, "dev", "alpha", begin)
	if err != nil || again.ID != first.ID {
		t.Fatalf("retried begin = %#v, %v; want the same session", again, err)
	}
	begin.Params = map[string]string{"phone": "5215500000002"}
	if _, err := manager.BeginSetup(ctx, "dev", "alpha", begin); !errors.Is(
		err, botflota.ErrConflict,
	) {
		t.Fatalf("begin with another phone = %v, want ErrConflict", err)
	}
}
