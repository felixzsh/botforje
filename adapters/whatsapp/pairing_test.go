package whatsapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
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

// waitSetup polls one session through the neutral read contract until it
// reaches want, so a test never inspects the manager's internal state.
func waitSetup(
	t *testing.T, manager *PairManager, scope, bot, id string,
	want botflota.SetupState,
) *botflota.SetupSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, err := manager.ReadSetup(context.Background(), scope, bot, id)
		if err == nil && session.State == want {
			return session
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session never reached %s", want)
	return nil
}

// TestSetupSuccessActivatesTheBot: a committed setup activates the bot before
// the session reports its success (ADR 0053).
func TestSetupSuccessActivatesTheBot(t *testing.T) {
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
		return PairResult{Identity: waKey("5215500000001")}, nil
	}
	started, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	final := waitSetup(t, manager, "dev", "alpha", started.ID, botflota.SetupSucceeded)
	if final.ErrorCode != "" || final.Step != nil {
		t.Fatalf("final = %#v, want a clean success", final)
	}
	if !activated {
		t.Fatal("the bot was not activated before the session reported success")
	}
}

// TestSetupFailureIsSanitized: a setup failure reports only a sanitized code,
// never the provider text behind it (ADR 0053).
func TestSetupFailureIsSanitized(t *testing.T) {
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(context.Context, PairOptions) (PairResult, error) {
		return PairResult{}, errors.New("scan rejected")
	}
	started, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	final := waitSetup(t, manager, "dev", "alpha", started.ID, botflota.SetupFailed)
	if final.ErrorCode != "setup_failed" {
		t.Fatalf("error code = %q, want the sanitized failure", final.ErrorCode)
	}
}

// TestSetupCloseStopsLiveSessions proves the shutdown contract: Close cancels
// the running setup, waits for its goroutine, and refuses new sessions, so no
// writer touches the stores after their owner closes them (ADR 0053).
func TestSetupCloseStopsLiveSessions(t *testing.T) {
	started := make(chan PairOptions, 1)
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, opts PairOptions) (PairResult, error) {
		started <- opts
		<-ctx.Done()
		return PairResult{}, ctx.Err()
	}
	session, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	<-started

	manager.Close()

	final := waitSetup(t, manager, "dev", "alpha", session.ID, botflota.SetupCancelled)
	if final.State != botflota.SetupCancelled {
		t.Fatalf("state = %s, want cancelled", final.State)
	}
	if _, err := manager.BeginSetup(context.Background(), "dev", "beta",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-2"},
	); !errors.Is(err, errPairingClosed) {
		t.Fatalf("BeginSetup after Close = %v, want errPairingClosed", err)
	}
}

// TestSetupCancelDuringActivationKeepsSuccess covers the race between a commit
// and its report: once the account is stored, a cancel in the activation window
// is ignored and the session still reports success (ADR 0053).
func TestSetupCancelDuringActivationKeepsSuccess(t *testing.T) {
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
		return PairResult{Identity: waKey("5215500000001")}, nil
	}
	started, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	<-inActivation

	if err := manager.CancelSetup(context.Background(), "dev", "alpha",
		started.ID); err != nil {
		t.Fatalf("CancelSetup: %v", err)
	}
	current, err := manager.ReadSetup(context.Background(), "dev", "alpha", started.ID)
	if err != nil {
		t.Fatalf("ReadSetup: %v", err)
	}
	if current.State == botflota.SetupCancelled {
		t.Fatal("the committed setup was rewritten to cancelled")
	}
	close(release)

	final := waitSetup(t, manager, "dev", "alpha", started.ID, botflota.SetupSucceeded)
	if final.State != botflota.SetupSucceeded {
		t.Fatalf("state = %s, want success", final.State)
	}
}

// TestEndPairAbortsBeforeSave: an unpair during the QR wait cancels the
// session, never activates the bot, and leaves the bot free for the setup that
// follows (ADR 0021).
func TestEndPairAbortsBeforeSave(t *testing.T) {
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
	session, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	<-started

	if err := manager.EndPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("EndPair: %v", err)
	}
	final := waitSetup(t, manager, "dev", "alpha", session.ID, botflota.SetupCancelled)
	if final.State != botflota.SetupCancelled {
		t.Fatalf("state = %s, want cancelled", final.State)
	}
	if activated {
		t.Fatal("an aborted setup activated the bot")
	}
	if err := manager.EndPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("second EndPair: %v, want a no-op", err)
	}
	// The unpair leaves no session behind: the next setup starts a fresh one.
	if _, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-2"}); err != nil {
		t.Fatalf("setup after EndPair: %v", err)
	}
}

// TestEndPairKeepsTheStoredLink: once the account is stored, an unpair only
// ends the import wait — the session still reports the link that exists — and
// the bot is not activated because the unpair removes it right after
// (ADR 0021, ADR 0053).
func TestEndPairKeepsTheStoredLink(t *testing.T) {
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
		return PairResult{Identity: waKey("5215500000001")}, ctx.Err()
	}
	session, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	<-saved

	if err := manager.EndPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("EndPair: %v", err)
	}
	final := waitSetup(t, manager, "dev", "alpha", session.ID, botflota.SetupSucceeded)
	if final.State != botflota.SetupSucceeded {
		t.Fatalf("state = %s, want the stored link reported as success", final.State)
	}
	if activated {
		t.Fatal("the unpair activated a bot it removes right after")
	}
}

// TestEndPairHonorsItsContext: a setup that ignores the cancellation must not
// hold the unpair past its deadline; the end itself still applies, and the wait
// reports the expired context.
func TestEndPairHonorsItsContext(t *testing.T) {
	started := make(chan struct{}, 1)
	stuck := make(chan struct{})
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(context.Context, PairOptions) (PairResult, error) {
		started <- struct{}{}
		<-stuck
		return PairResult{}, nil
	}
	if _, err := manager.BeginSetup(context.Background(), "dev", "alpha",
		botflota.SetupBegin{Method: "qr", RequestKey: "key-1"}); err != nil {
		t.Fatalf("BeginSetup: %v", err)
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
