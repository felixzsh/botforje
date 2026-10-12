package whatsapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// TestPairManagerCloseWaitsForPairingToFinish: Close cancels every session
// and waits for its goroutine before stores can be closed, including work
// that remains after cancellation (ADR 0025).
func TestPairManagerCloseWaitsForPairingToFinish(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, _ PairOptions) (PairResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		<-release
		return PairResult{}, ctx.Err()
	}
	defer manager.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("pairing goroutine did not start")
	}

	closed := make(chan struct{})
	go func() { manager.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned before the pairing goroutine finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the pairing goroutine finished")
	}
}

// TestPairManagerCloseWaitsForPublishedSession covers the interval after a
// session is visible but before its goroutine starts: Close must not return
// until that session reports completion.
func TestPairManagerCloseWaitsForPublishedSession(t *testing.T) {
	manager := testPairManager(t, PairManagerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	session := &pairingSession{
		neutral: botflota.SetupSession{
			Bot:   botflota.BotRef{Scope: "dev", Bot: "alpha"},
			State: botflota.SetupStarting,
		},
		ctx: ctx, cancel: cancel, timer: time.NewTimer(time.Hour),
		closed: make(chan struct{}),
	}
	defer session.timer.Stop()
	defer func() {
		select {
		case <-session.closed:
		default:
			close(session.closed)
		}
	}()
	manager.mu.Lock()
	manager.sessions[botflota.BotRef{Scope: "dev", Bot: "alpha"}] = session
	manager.mu.Unlock()

	closed := make(chan struct{})
	go func() { manager.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned before the published session completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(session.closed)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the session completed")
	}
}

// TestPairManagerEndPairWaitsForCancelledSession covers a final state whose
// pairing goroutine still has work to finish: unpair waits for that work and
// a new session cannot overlap it (ADR 0021, ADR 0025).
func TestPairManagerEndPairWaitsForCancelledSession(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	manager := testPairManager(t, PairManagerOptions{})
	manager.pair = func(ctx context.Context, _ PairOptions) (PairResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		<-release
		return PairResult{}, ctx.Err()
	}
	defer manager.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("pairing goroutine did not start")
	}
	if err := manager.CancelPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("CancelPair: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := manager.EndPair(ctx, "dev", "alpha")
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("EndPair = %v, want to wait for the cancelled goroutine", err)
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); !errors.Is(
		err, botflota.ErrBusy,
	) {
		t.Fatalf("StartPair before old session ends = %v, want ErrBusy", err)
	}

	close(release)
	if err := manager.EndPair(context.Background(), "dev", "alpha"); err != nil {
		t.Fatalf("EndPair after cancellation completed: %v", err)
	}
	if _, err := manager.StartPair(
		context.Background(), "dev", "alpha", botflota.PairRequest{}); err != nil {
		t.Fatalf("StartPair after old session ended: %v", err)
	}
}
