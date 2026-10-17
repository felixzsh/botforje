package testchannel_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/testchannel"
	"github.com/felixzsh/botflota/channels"
)

// TestSetupDrivesTheNeutralSessionMachinery proves the adapter links an
// account with the shared step kinds and session contract, commits a binding,
// and never stores the submitted credential (ADR 0053).
func TestSetupDrivesTheNeutralSessionMachinery(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	def := testchannel.Definition()
	deps := testchannel.Deps{Bindings: db.Bindings()}
	def.Setup = testchannel.Setup(deps)
	lifecycle, runner, err := def.Setup(channels.Bind{
		Scope: "conf", Bot: "bot",
		Options: testchannel.BotOptions{Namespace: "profile-a"},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	t.Run("methods", func(t *testing.T) {
		methods, err := runner.SetupMethods(ctx, "conf", "bot")
		if err != nil {
			t.Fatalf("SetupMethods: %v", err)
		}
		if len(methods) != 1 || methods[0] != testchannel.SetupMethodCredential {
			t.Fatalf("methods = %v", methods)
		}
	})

	session, err := runner.BeginSetup(ctx, "conf", "bot", botflota.SetupBegin{
		Method: testchannel.SetupMethodCredential, RequestKey: "k1",
	})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	assertInputStep(t, session)

	t.Run("begin is idempotent", func(t *testing.T) {
		again, err := runner.BeginSetup(ctx, "conf", "bot", botflota.SetupBegin{
			Method: testchannel.SetupMethodCredential, RequestKey: "k1",
		})
		if err != nil {
			t.Fatalf("BeginSetup again: %v", err)
		}
		if again.ID != session.ID {
			t.Fatalf("session id = %q, want %q", again.ID, session.ID)
		}
	})

	t.Run("stale revision is refused", func(t *testing.T) {
		_, err := runner.InputSetup(ctx, "conf", "bot", session.ID,
			botflota.SetupInput{
				Revision: session.Revision + 5,
				Values:   map[string]string{testchannel.SetupFieldCredential: "x"},
			})
		if !errors.Is(err, botflota.ErrConflict) {
			t.Fatalf("InputSetup err = %v, want ErrConflict", err)
		}
	})

	t.Run("missing credential is refused", func(t *testing.T) {
		_, err := runner.InputSetup(ctx, "conf", "bot", session.ID,
			botflota.SetupInput{Revision: session.Revision, Values: map[string]string{}})
		if !errors.Is(err, botflota.ErrInvalidParam) {
			t.Fatalf("InputSetup err = %v, want ErrInvalidParam", err)
		}
	})

	const credential = "super-secret-credential"
	done, err := runner.InputSetup(ctx, "conf", "bot", session.ID,
		botflota.SetupInput{
			Revision: session.Revision,
			Values:   map[string]string{testchannel.SetupFieldCredential: credential},
		})
	if err != nil {
		t.Fatalf("InputSetup: %v", err)
	}
	if done.State != botflota.SetupSucceeded {
		t.Fatalf("state = %s, want succeeded", done.State)
	}

	binding, err := db.Bindings().Get(ctx, "conf", "bot")
	if err != nil {
		t.Fatalf("read committed binding: %v", err)
	}
	if binding.Provider != testchannel.Provider ||
		binding.Mode != testchannel.Mode ||
		binding.Namespace != "profile-a" {
		t.Fatalf("binding = %+v, want the declared identity", binding)
	}
	if strings.Contains(binding.Identity.Subject, credential) {
		t.Fatal("the submitted credential was persisted as the subject")
	}

	t.Run("inspect reports configured", func(t *testing.T) {
		status, err := lifecycle.InspectBinding(ctx, "conf", "bot")
		if err != nil {
			t.Fatalf("InspectBinding: %v", err)
		}
		if status.Condition != botflota.ReadinessConfigured {
			t.Fatalf("condition = %s, want configured", status.Condition)
		}
	})

	t.Run("detach forgets the binding only", func(t *testing.T) {
		if err := lifecycle.DetachBinding(ctx, "conf", "bot"); err != nil {
			t.Fatalf("DetachBinding: %v", err)
		}
		if _, err := db.Bindings().Get(ctx, "conf", "bot"); !errors.Is(err, botflota.ErrNotFound) {
			t.Fatalf("binding survived detach: %v", err)
		}
		status, err := lifecycle.InspectBinding(ctx, "conf", "bot")
		if err != nil {
			t.Fatalf("InspectBinding after detach: %v", err)
		}
		if status.Condition != botflota.ReadinessUnconfigured {
			t.Fatalf("condition = %s, want unconfigured", status.Condition)
		}
	})
}

// TestPrepareRequiresACommittedBinding proves the readiness hook is honest: an
// unlinked bot is skipped, and the same bot joins once setup commits a binding
// (ADR 0022, ADR 0053).
func TestPrepareRequiresACommittedBinding(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	deps := testchannel.Deps{Bindings: db.Bindings()}
	prepare := testchannel.Prepare(deps)
	bind := channels.Bind{Scope: "conf", Bot: "bot", Options: testchannel.BotOptions{}}
	ready, state, err := prepare(ctx, bind)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if ready || state != "no account linked" {
		t.Fatalf("ready = %t state = %q, want an unlinked bot refused", ready, state)
	}
	binding := botflota.Binding{
		Scope: "conf", Bot: "bot",
		Provider: testchannel.Provider, Adapter: testchannel.Kind,
		Mode: testchannel.Mode, Namespace: "profile-a",
		Identity: botflota.IdentityKey{Provider: testchannel.Provider, Subject: "acct"},
	}
	if err := db.Bindings().Commit(ctx, &binding); err != nil {
		t.Fatalf("commit binding: %v", err)
	}
	ready, state, err = prepare(ctx, bind)
	if err != nil {
		t.Fatalf("Prepare after commit: %v", err)
	}
	if !ready || state != string(botflota.ReadinessConfigured) {
		t.Fatalf("ready = %t state = %q, want ready", ready, state)
	}
}

func assertInputStep(t *testing.T, session *botflota.SetupSession) {
	t.Helper()
	if session.State != botflota.SetupAwaitingInput {
		t.Fatalf("state = %s, want awaiting_input", session.State)
	}
	if session.Step == nil || session.Step.Kind != botflota.SetupStepInput {
		t.Fatalf("step = %+v, want an input step", session.Step)
	}
	if len(session.Step.Fields) != 1 ||
		session.Step.Fields[0].Name != testchannel.SetupFieldCredential {
		t.Fatalf("fields = %+v, want the credential field", session.Step.Fields)
	}
}

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "botflota.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// countingBindings counts commits, so a concurrency test can prove a repeated
// input request key submits the credential once (ADR 0053).
type countingBindings struct {
	botflota.BindingStore

	mu      sync.Mutex
	commits int
}

func (c *countingBindings) Commit(
	ctx context.Context, binding *botflota.Binding,
) error {
	c.mu.Lock()
	c.commits++
	c.mu.Unlock()
	return c.BindingStore.Commit(ctx, binding)
}

func (c *countingBindings) commitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commits
}

// newSetupRunner installs the adapter's real setup machinery over one test
// database and returns the lifecycle beside the runner.
func newSetupRunner(
	t *testing.T, bindings botflota.BindingStore,
) (botflota.AccountLifecycle, botflota.SetupRunner) {
	t.Helper()
	def := testchannel.Definition()
	def.Setup = testchannel.Setup(testchannel.Deps{Bindings: bindings})
	lifecycle, runner, err := def.Setup(channels.Bind{
		Scope: "conf", Bot: "bot",
		Options: testchannel.BotOptions{Namespace: "profile-a"},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return lifecycle, runner
}

// TestSetupInputRequestKeyIsIdempotent proves a repeated input request key
// returns the recorded outcome and never submits the credential twice, even
// after the session committed (ADR 0053).
func TestSetupInputRequestKeyIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	counted := &countingBindings{BindingStore: db.Bindings()}
	_, runner := newSetupRunner(t, counted)

	session, err := runner.BeginSetup(ctx, "conf", "bot", botflota.SetupBegin{
		Method: testchannel.SetupMethodCredential, RequestKey: "begin-1",
	})
	if err != nil {
		t.Fatalf("BeginSetup: %v", err)
	}
	input := botflota.SetupInput{
		Revision: session.Revision, RequestKey: "input-1",
		Values: map[string]string{
			testchannel.SetupFieldCredential: "super-secret-credential",
		},
	}
	first, err := runner.InputSetup(ctx, "conf", "bot", session.ID, input)
	if err != nil {
		t.Fatalf("InputSetup: %v", err)
	}
	if first.State != botflota.SetupSucceeded {
		t.Fatalf("state = %s, want succeeded", first.State)
	}

	second, err := runner.InputSetup(ctx, "conf", "bot", session.ID, input)
	if err != nil {
		t.Fatalf("a repeated input request key was refused: %v", err)
	}
	if second.State != botflota.SetupSucceeded || second.Revision != first.Revision {
		t.Fatalf("retry = %+v, want the recorded outcome %+v", second, first)
	}
	if got := counted.commitCount(); got != 1 {
		t.Fatalf("commits = %d, want the credential submitted once", got)
	}

	different := input
	different.RequestKey = "input-2"
	if _, err := runner.InputSetup(
		ctx, "conf", "bot", session.ID, different,
	); !errors.Is(err, botflota.ErrConflict) {
		t.Fatalf("a new key on a committed session = %v, want ErrConflict", err)
	}
}

// TestSetupConcurrentMutationsStaySerialized proves the session contract holds
// under concurrent begin/input/cancel/detach: one setup mutation per bot runs
// at a time, a repeated input key commits once, and no mutation leaves a
// plaintext credential or a stuck peer (ADR 0053).
func TestSetupConcurrentMutationsStaySerialized(t *testing.T) {
	t.Run("repeated input key commits once", func(t *testing.T) {
		ctx := context.Background()
		db := openTestDB(t)
		counted := &countingBindings{BindingStore: db.Bindings()}
		_, runner := newSetupRunner(t, counted)
		session, err := runner.BeginSetup(ctx, "conf", "bot", botflota.SetupBegin{
			Method: testchannel.SetupMethodCredential, RequestKey: "begin-1",
		})
		if err != nil {
			t.Fatalf("BeginSetup: %v", err)
		}
		const workers = 8
		states := make([]botflota.SetupState, workers)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				done, err := runner.InputSetup(
					ctx, "conf", "bot", session.ID, botflota.SetupInput{
						Revision: session.Revision, RequestKey: "same-key",
						Values: map[string]string{
							testchannel.SetupFieldCredential: "credential",
						},
					})
				if err != nil {
					t.Errorf("InputSetup %d: %v", i, err)
					return
				}
				states[i] = done.State
			}(i)
		}
		wg.Wait()
		for i, state := range states {
			if state != botflota.SetupSucceeded {
				t.Fatalf("worker %d state = %s, want succeeded", i, state)
			}
		}
		if got := counted.commitCount(); got != 1 {
			t.Fatalf("commits = %d, want exactly one", got)
		}
	})

	t.Run("begin input cancel and detach race", func(t *testing.T) {
		ctx := context.Background()
		db := openTestDB(t)
		lifecycle, runner := newSetupRunner(t, db.Bindings())
		const workers = 8
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				session, err := runner.BeginSetup(
					ctx, "conf", "bot", botflota.SetupBegin{
						Method:     testchannel.SetupMethodCredential,
						RequestKey: fmt.Sprintf("begin-%d", i),
					})
				if err != nil {
					return
				}
				if i%2 == 1 {
					if err := runner.CancelSetup(
						ctx, "conf", "bot", session.ID,
					); err != nil && !errors.Is(err, botflota.ErrNotFound) {
						t.Errorf("CancelSetup %d: %v", i, err)
					}
					return
				}
				_, err = runner.InputSetup(
					ctx, "conf", "bot", session.ID, botflota.SetupInput{
						Revision:   session.Revision,
						RequestKey: fmt.Sprintf("input-%d", i),
						Values: map[string]string{
							testchannel.SetupFieldCredential: fmt.Sprintf("cred-%d", i),
						},
					})
				if err != nil && !errors.Is(err, botflota.ErrConflict) &&
					!errors.Is(err, botflota.ErrNotFound) {
					t.Errorf("InputSetup %d: %v", i, err)
				}
			}(i)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := lifecycle.DetachBinding(ctx, "conf", "bot"); err != nil {
				t.Errorf("DetachBinding: %v", err)
			}
		}()
		wg.Wait()

		binding, err := db.Bindings().Get(ctx, "conf", "bot")
		switch {
		case err == nil:
			if !strings.HasPrefix(binding.Identity.Subject, "acct-") {
				t.Fatalf("subject = %q, want an opaque account subject",
					binding.Identity.Subject)
			}
			if strings.Contains(binding.Identity.Subject, "cred-") {
				t.Fatal("a plaintext credential reached the binding")
			}
		case errors.Is(err, botflota.ErrNotFound):
		default:
			t.Fatalf("read binding after the race: %v", err)
		}
	})
}
