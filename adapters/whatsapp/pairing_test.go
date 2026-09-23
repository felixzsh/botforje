package whatsapp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

func testPairManager(t *testing.T, opts PairManagerOptions) *PairManager {
	t.Helper()
	if opts.Build == nil {
		opts.Build = func(string, string, bool) (PairOptions, error) {
			return PairOptions{}, nil
		}
	}
	manager := NewPairManager(opts)
	if manager == nil {
		t.Fatal("NewPairManager returned nil")
	}
	return manager
}

func waitSession(
	t *testing.T, manager *PairManager, scope, bot string, want botflota.PairState,
	final bool,
) botflota.PairingSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, err := manager.CurrentPair(context.Background(), scope, bot)
		if err == nil && (session.State == want || (final && session.Final())) {
			return *session
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session never reached %s", want)
	return botflota.PairingSession{}
}

// TestPairManagerSession walks one session from a code to a cancellation,
// including the busy and unknown cases around it.
func TestPairManagerSession(t *testing.T) {
	started := make(chan PairOptions, 1)
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		started <- opts
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}

	session, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{})
	if err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	if session.State != botflota.PairPending {
		t.Fatalf("state = %s, want pending", session.State)
	}
	var pairOpts PairOptions
	select {
	case pairOpts = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the pairing never started")
	}
	pairOpts.RenderQR("qr-payload")

	waiting := waitSession(t, manager, "dev", "alpha", botflota.PairWaiting, false)
	if waiting.Code != "qr-payload" {
		t.Fatalf("code = %q, want qr-payload", waiting.Code)
	}

	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{},
	); !errors.Is(err, botflota.ErrBusy) {
		t.Fatalf("second StartPair = %v, want ErrBusy", err)
	}

	if err := manager.CancelPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("CancelPair: %v", err)
	}
	final := waitSession(t, manager, "dev", "alpha", botflota.PairCancelled, true)
	if final.State != botflota.PairCancelled || final.Code != "" {
		t.Fatalf("final = %#v", final)
	}
	if err := manager.CancelPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("second CancelPair = %v, want a no-op", err)
	}
	if err := manager.CancelPair(
		context.Background(), "dev", "ghost",
	); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("CancelPair unknown = %v, want ErrNotFound", err)
	}
}

func TestPairManagerPairingSucceeds(t *testing.T) {
	activated := false
	manager := testPairManager(t, PairManagerOptions{
		OnPaired: func(ctx context.Context, scope, bot string) error {
			if scope != "dev" || bot != "alpha" {
				t.Errorf("OnPaired = %s/%s, want dev/alpha", scope, bot)
			}
			activated = true
			return nil
		},
	})
	manager.pair = func(_ context.Context, opts PairOptions) (PairResult, error) {
		opts.RenderQR("code")
		opts.OnSaved()
		return PairResult{
			JID: "5215500000001@s.whatsapp.net",
			History: ingest.ImportTotals{
				Conversations: 3, Imported: 40, Duplicate: 2, Skipped: 1,
			},
			Contacts: 7,
			Elapsed:  90 * time.Second,
		}, nil
	}

	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	final := waitSession(t, manager, "dev", "alpha", botflota.PairSucceeded, true)
	if final.Code != "" || final.Error != "" {
		t.Fatalf("final = %#v", final)
	}
	result := final.Result
	if result.Phone != "5215500000001" || result.Chats != 3 ||
		result.Messages != 40 || result.Duplicate != 2 || result.Skipped != 1 ||
		result.Contacts != 7 || result.Elapsed != 90*time.Second {
		t.Fatalf("result = %#v", result)
	}
	if !activated {
		t.Fatal("the bot was not activated before the session reported paired")
	}
}

func TestPairManagerExpires(t *testing.T) {
	manager := testPairManager(t, PairManagerOptions{Lifetime: 20 * time.Millisecond})
	manager.pair = func(ctx context.Context, _ PairOptions) (PairResult, error) {
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	final := waitSession(t, manager, "dev", "alpha", botflota.PairExpired, true)
	if final.State != botflota.PairExpired {
		t.Fatalf("final = %#v", final)
	}
}

func TestPairManagerFailure(t *testing.T) {
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(context.Context, PairOptions) (PairResult, error) {
		return PairResult{}, errors.New("scan rejected")
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	final := waitSession(t, manager, "dev", "alpha", botflota.PairFailed, true)
	if !strings.Contains(final.Error, "scan rejected") {
		t.Fatalf("error = %q, want the pairing failure", final.Error)
	}
}

// TestPairManagerCloseStopsLiveSessions proves the shutdown contract: Close
// cancels the running pairing, waits for its goroutine, and refuses new
// sessions, so no writer touches the stores after their owner closes them.
func TestPairManagerCloseStopsLiveSessions(t *testing.T) {
	started := make(chan PairOptions, 1)
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		started <- opts
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	<-started

	manager.Close()

	final := waitSession(t, manager, "dev", "alpha", botflota.PairCancelled, true)
	if final.State != botflota.PairCancelled {
		t.Fatalf("state = %s, want cancelled", final.State)
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "beta", botflota.PairRequest{},
	); !errors.Is(err, errPairingClosed) {
		t.Fatalf("StartPair after Close = %v, want errPairingClosed", err)
	}
}

// TestPairManagerCancelDuringActivationKeepsSuccess covers the race between a
// save and its report: once the account is stored, a cancel in the activation
// window is ignored and the session still reports paired (ADR 0025).
func TestPairManagerCancelDuringActivationKeepsSuccess(t *testing.T) {
	inActivation := make(chan struct{})
	release := make(chan struct{})
	manager := testPairManager(t, PairManagerOptions{
		OnPaired: func(context.Context, string, string) error {
			close(inActivation)
			<-release
			return nil
		},
	})
	manager.pair = func(_ context.Context, opts PairOptions) (PairResult, error) {
		opts.OnSaved()
		return PairResult{JID: "5215500000001@s.whatsapp.net"}, nil
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	<-inActivation

	if err := manager.CancelPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("CancelPair: %v", err)
	}
	current, err := manager.CurrentPair(context.Background(), "dev", "alpha")
	if err != nil {
		t.Fatalf("CurrentPair: %v", err)
	}
	if current.State == botflota.PairCancelled {
		t.Fatal("the saved pairing was rewritten to cancelled")
	}
	close(release)

	final := waitSession(t, manager, "dev", "alpha", botflota.PairSucceeded, true)
	if final.State != botflota.PairSucceeded {
		t.Fatalf("state = %s, want paired", final.State)
	}
	if final.Result.Phone != "5215500000001" {
		t.Fatalf("result = %#v, want the stored phone", final.Result)
	}
}

// TestPairManagerEndPairAbortsBeforeSave: an unpair during the QR wait
// cancels the session, never activates the bot, and leaves the bot free for
// the re-pair that follows (ADR 0021).
func TestPairManagerEndPairAbortsBeforeSave(t *testing.T) {
	started := make(chan PairOptions, 1)
	activated := false
	manager := testPairManager(t, PairManagerOptions{
		OnPaired: func(context.Context, string, string) error {
			activated = true
			return nil
		},
	})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		started <- opts
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	defer manager.Close()
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	<-started

	if err := manager.EndPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("EndPair: %v", err)
	}
	final := waitSession(t, manager, "dev", "alpha", botflota.PairCancelled, true)
	if final.State != botflota.PairCancelled {
		t.Fatalf("state = %s, want cancelled", final.State)
	}
	if activated {
		t.Fatal("an aborted pairing activated the bot")
	}
	if err := manager.EndPair(
		context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("second EndPair: %v, want a no-op", err)
	}
	// The unpair leaves no session behind: the re-pair starts a fresh one.
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("re-pair after EndPair: %v", err)
	}
}

// TestPairManagerEndPairKeepsTheStoredLink: once the account is stored, an
// unpair only ends the import wait — the session still reports the link that
// exists — and the bot is not activated because the unpair removes it right
// after (ADR 0021, ADR 0025).
func TestPairManagerEndPairKeepsTheStoredLink(t *testing.T) {
	saved := make(chan struct{})
	activated := false
	manager := testPairManager(t, PairManagerOptions{
		OnPaired: func(context.Context, string, string) error {
			activated = true
			return nil
		},
	})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		opts.OnSaved()
		close(saved)
		<-ctx.Done()
		return PairResult{JID: "5215500000001@s.whatsapp.net"}, ctx.Err()
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	<-saved

	if err := manager.EndPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("EndPair: %v", err)
	}
	final := waitSession(t, manager, "dev", "alpha", botflota.PairSucceeded, true)
	if final.State != botflota.PairSucceeded {
		t.Fatalf("state = %s, want the stored link reported as paired",
			final.State)
	}
	if final.Result.Phone != "5215500000001" {
		t.Fatalf("result = %#v, want the stored phone", final.Result)
	}
	if activated {
		t.Fatal("the unpair activated a bot it removes right after")
	}
}

// TestPairManagerEndPairHonorsItsContext: a pairing that ignores the
// cancellation must not hold the unpair past its deadline; the end itself
// still applies, and the wait reports the expired context.
func TestPairManagerEndPairHonorsItsContext(t *testing.T) {
	started := make(chan struct{}, 1)
	stuck := make(chan struct{})
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(context.Context, PairOptions) (PairResult, error) {
		started <- struct{}{}
		<-stuck
		return PairResult{}, nil
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.EndPair(ctx, "dev", "alpha"); err == nil {
		t.Fatal("EndPair under an expired context must report it")
	}
	close(stuck)
	manager.Close()
}
