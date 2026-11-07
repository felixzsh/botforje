package whatsapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
		opts.OnPending()
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
		pending.Step.Approval == nil || pending.Step.Approval.Instruction == "" {
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
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		opts.OnCode("ABCD-EFGH")
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
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

// TestSetupPendingInstructionFitsTheMethod: the reported step tells the truth
// of the method that reached it. A code link can still be finished on the
// phone that is linking; a QR link cannot answer the ceremony from this client
// at all (ADR 0028).
func TestSetupPendingInstructionFitsTheMethod(t *testing.T) {
	cases := []struct {
		name    string
		begin   botflota.SetupBegin
		want    string
		notWant string
	}{
		{
			"code keeps the phone handoff",
			botflota.SetupBegin{
				Method: SetupMethodCode, RequestKey: "key-1",
				Params: map[string]string{"phone": "5215500000001"},
			},
			"phone that is linking",
			"cannot be linked from here",
		},
		{
			"qr says it cannot be linked here",
			botflota.SetupBegin{Method: SetupMethodQR, RequestKey: "key-2"},
			"cannot be linked from here",
			"phone that is linking",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager, _ := testSetupManager(t, PairManagerOptions{})
			manager.pair = func(ctx context.Context, opts PairOptions) (
				PairResult, error,
			) {
				opts.OnPending()
				<-ctx.Done()
				return PairResult{}, ctx.Err()
			}
			started, err := manager.BeginSetup(context.Background(), "dev",
				"alpha", tc.begin)
			if err != nil {
				t.Fatalf("BeginSetup: %v", err)
			}
			pending := waitSetup(t, manager, "dev", "alpha", started.ID,
				botflota.SetupAwaitingApproval)
			if pending.Step == nil || pending.Step.Approval == nil {
				t.Fatalf("step = %#v, want the reported instruction", pending.Step)
			}
			instruction := pending.Step.Approval.Instruction
			if !strings.Contains(instruction, tc.want) ||
				strings.Contains(instruction, tc.notWant) {
				t.Fatalf("instruction = %q, want %q and not %q",
					instruction, tc.want, tc.notWant)
			}
		})
	}
}

// TestSetupNewChallengeReplacesAPendingStep: a challenge the provider issues
// after a pending report is published again, so the owner can still obtain the
// code that completes the link; the pending step stays visible until it is
// (ADR 0028).
func TestSetupNewChallengeReplacesAPendingStep(t *testing.T) {
	given := make(chan PairOptions, 1)
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		given <- opts
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
	opts := <-given

	opts.OnCode("ABCD-EFGH")
	first := waitSession(t, manager, "dev", "alpha", started.ID,
		func(session *botflota.SetupSession) bool {
			return session.Step != nil && session.Step.Display != nil &&
				session.Step.Display.Payload == "ABCD-EFGH"
		})
	opts.OnPending()
	pending := waitSession(t, manager, "dev", "alpha", started.ID,
		func(session *botflota.SetupSession) bool {
			return session.Step != nil &&
				session.Step.Kind == botflota.SetupStepApproval
		})
	if pending.Revision <= first.Revision {
		t.Fatalf("revision = %d, want a bump for the pending report",
			pending.Revision)
	}

	opts.OnCode("IJKL-MNOP")
	refreshed := waitSession(t, manager, "dev", "alpha", started.ID,
		func(session *botflota.SetupSession) bool {
			return session.Step != nil && session.Step.Display != nil &&
				session.Step.Display.Payload == "IJKL-MNOP"
		})
	if refreshed.Revision <= pending.Revision {
		t.Fatalf("revision = %d, want a bump for the new challenge",
			refreshed.Revision)
	}
	if refreshed.State != botflota.SetupAwaitingApproval {
		t.Fatalf("state = %s, want the code method's approval wait",
			refreshed.State)
	}
}

// TestSetupQRRotationKeepsThePendingStep: on a QR link a rotation is not a new
// chance to finish, so the session keeps answering the reported instruction
// and publishes no further challenge that would fail the same way (ADR 0028).
func TestSetupQRRotationKeepsThePendingStep(t *testing.T) {
	given := make(chan PairOptions, 1)
	manager, _ := testSetupManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		given <- opts
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	started, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: SetupMethodQR, RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	opts := <-given

	opts.OnCode("qr-1")
	first := waitSession(t, manager, "dev", "alpha", started.ID,
		func(session *botflota.SetupSession) bool {
			return session.Step != nil && session.Step.Display != nil &&
				session.Step.Display.Payload == "qr-1"
		})
	if first.Step.Display.Format != botflota.SetupDisplayQR {
		t.Fatalf("display = %#v, want the scanned QR challenge", first.Step.Display)
	}
	opts.OnPending()
	pending := waitSession(t, manager, "dev", "alpha", started.ID,
		func(session *botflota.SetupSession) bool {
			return session.Step != nil &&
				session.Step.Kind == botflota.SetupStepApproval
		})
	instruction := pending.Step.Approval.Instruction

	// The provider keeps rotating challenges after it asked for the passkey.
	opts.OnCode("qr-2")
	rotated := waitSession(t, manager, "dev", "alpha", started.ID,
		func(session *botflota.SetupSession) bool {
			return session.Revision > pending.Revision
		})
	if rotated.Step == nil ||
		rotated.Step.Kind != botflota.SetupStepApproval ||
		rotated.Step.Approval == nil ||
		rotated.Step.Approval.Instruction != instruction {
		t.Fatalf("step after rotation = %#v, want the same pending instruction",
			rotated.Step)
	}
	for i := 0; i < 5; i++ {
		current := readNeutral(t, manager, "dev", "alpha", started.ID)
		if current.Step == nil ||
			current.Step.Kind != botflota.SetupStepApproval {
			t.Fatalf("rotation published %#v, want no new challenge",
				current.Step)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSetupRequirementsNameTheCodePhone: discovery answers what each method
// needs to begin, so a client does not have to learn the phone requirement
// from a rejected request (ADR 0053).
func TestSetupRequirementsNameTheCodePhone(t *testing.T) {
	manager := testPairManager(t, PairManagerOptions{})
	requirements, err := manager.SetupRequirements(context.Background(),
		"dev", "alpha")
	if err != nil {
		t.Fatalf("SetupRequirements: %v", err)
	}
	want := []botflota.SetupRequirement{
		{Method: SetupMethodQR},
		{Method: SetupMethodCode, Params: []string{"phone"}},
	}
	if !reflect.DeepEqual(requirements, want) {
		t.Fatalf("requirements = %#v, want %#v", requirements, want)
	}
}
