package botflota_test

// The neutral binding-lifecycle gate (ADR 0053, docs/channels/phases.md P3):
// executable fakes complete QR, credential-only, multi-step approval, and
// local-profile setup through the same session contract, with per-bot
// serialization, idempotent request keys, staged commits, and generations
// that survive detach and replacement. No phone field and no universal QR
// appear anywhere in these shapes.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// fakeBackend is one shared backend two fixed bindings can hold: profiles
// live here, while each bot keeps its own identity, generation, and session.
// Selecting one bot's context never touches the other's.
type fakeBackend struct {
	mu       sync.Mutex
	profiles map[string]bool
	selected map[botflota.BotRef]string
	// events simulates the provider side: a test confirms a pending session,
	// which the runner observes like a provider event.
	events chan botflota.BotRef
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		profiles: map[string]bool{"main": true},
		selected: make(map[botflota.BotRef]string),
		events:   make(chan botflota.BotRef, 16),
	}
}

func (b *fakeBackend) confirm(ref botflota.BotRef) { b.events <- ref }

// fakeBinding is the backend's committed association for one bot.
type fakeBinding struct {
	provider   string
	adapter    string
	mode       string
	namespace  string
	identity   string
	generation int64
}

// fakeSession is one staged setup: credentials wait here until the provider
// confirms and the local commit lands.
type fakeSession struct {
	session    botflota.SetupSession
	requestKey string
	inputKeys  map[string]int64
	staged     *fakeBinding
	approved   bool
	closed     chan struct{}
}

type fakeRunner struct {
	backend *fakeBackend
	methods []string

	mu          sync.Mutex
	bindings    map[botflota.BotRef]*fakeBinding
	generations map[botflota.BotRef]int64
	sessions    map[botflota.BotRef]*fakeSession
	history     map[botflota.BotRef]int
	activations []botflota.BotRef
	activated   map[string]int
	seq         int64
	now         time.Time
	lifetime    time.Duration
	closed      bool
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		backend:     newFakeBackend(),
		methods:     []string{"qr", "token", "approval", "profile"},
		bindings:    make(map[botflota.BotRef]*fakeBinding),
		generations: make(map[botflota.BotRef]int64),
		sessions:    make(map[botflota.BotRef]*fakeSession),
		history:     make(map[botflota.BotRef]int),
		activated:   make(map[string]int),
		now:         time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC),
		lifetime:    15 * time.Minute,
	}
}

func (r *fakeRunner) SetupMethods(
	_ context.Context, _, _ string,
) ([]string, error) {
	return append([]string(nil), r.methods...), nil
}

func (r *fakeRunner) BeginSetup(
	_ context.Context, scope, bot string, req botflota.SetupBegin,
) (*botflota.SetupSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("fake: runner is closed")
	}
	known := false
	for _, method := range r.methods {
		if req.Method == "" || req.Method == method {
			known = true
		}
	}
	if !known {
		return nil, fmt.Errorf("fake: unsupported setup method %q", req.Method)
	}
	method := req.Method
	if method == "" {
		method = r.methods[0]
	}
	ref := botflota.BotRef{Scope: scope, Bot: bot}
	if current, ok := r.sessions[ref]; ok {
		if current.requestKey == req.RequestKey && req.RequestKey != "" {
			if current.session.Method == method {
				published := current.session
				return &published, nil
			}
			return nil, botflota.ErrConflict
		}
		if !current.session.Final() {
			return nil, botflota.ErrBusy
		}
	}
	r.seq++
	session := botflota.SetupSession{
		Bot: ref, ID: fmt.Sprintf("session-%d", r.seq),
		Method: method, Mode: "token-mode",
		State: botflota.SetupAwaitingInput, Revision: 1,
		StartedAt: r.now, UpdatedAt: r.now, ExpiresAt: r.now.Add(r.lifetime),
	}
	entry := &fakeSession{
		session:    session,
		requestKey: req.RequestKey,
		inputKeys:  make(map[string]int64),
		staged: &fakeBinding{
			provider: "fake", adapter: "fake", mode: "token-mode",
			identity: scope + "/" + bot + "-account",
		},
		closed: make(chan struct{}),
	}
	switch method {
	case "qr":
		entry.session.Step = &botflota.SetupStep{
			Kind: botflota.SetupStepDisplay,
			Display: &botflota.SetupDisplay{
				Format: "qr", Payload: "QR:" + entry.session.ID,
				Hint: "scan the code",
			},
		}
	case "token":
		entry.session.Step = &botflota.SetupStep{
			Kind:   botflota.SetupStepInput,
			Fields: []botflota.SetupField{{Name: "token", Kind: botflota.SetupFieldCredential, Label: "API token"}},
		}
	case "approval":
		entry.session.Step = &botflota.SetupStep{
			Kind:   botflota.SetupStepInput,
			Fields: []botflota.SetupField{{Name: "login", Kind: botflota.SetupFieldIdentifier, Label: "login name"}},
		}
	case "profile":
		entry.session.Step = &botflota.SetupStep{
			Kind: botflota.SetupStepProfile,
			Profile: &botflota.SetupProfile{
				Methods:     []string{"managed"},
				Options:     []string{"main"},
				AllowCreate: true,
				Params:      []botflota.SetupField{{Name: "name", Kind: botflota.SetupFieldIdentifier, Label: "profile name"}},
			},
		}
	}
	r.sessions[ref] = entry
	published := entry.session
	return &published, nil
}

func (r *fakeRunner) ReadSetup(
	_ context.Context, scope, bot, sessionID string,
) (*botflota.SetupSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[botflota.BotRef{Scope: scope, Bot: bot}]
	if !ok || entry.session.ID != sessionID {
		return nil, botflota.ErrNotFound
	}
	published := entry.session
	return &published, nil
}

func (r *fakeRunner) InputSetup(
	_ context.Context, scope, bot, sessionID string, input botflota.SetupInput,
) (*botflota.SetupSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[botflota.BotRef{Scope: scope, Bot: bot}]
	if !ok || entry.session.ID != sessionID {
		return nil, botflota.ErrNotFound
	}
	if input.RequestKey != "" {
		if _, seen := entry.inputKeys[input.RequestKey]; seen {
			// The key was already answered: return the recorded outcome
			// instead of submitting the credential or code twice.
			published := entry.session
			return &published, nil
		}
	}
	if input.Revision != entry.session.Revision {
		return nil, botflota.ErrConflict
	}
	if entry.session.Final() {
		return nil, botflota.ErrNotFound
	}
	mark := func() {
		if input.RequestKey != "" {
			entry.inputKeys[input.RequestKey] = entry.session.Revision
		}
	}
	switch entry.session.Method {
	case "token":
		if input.Values["token"] == "" {
			return nil, errors.New("fake: token is required")
		}
		mark()
		// A credential is staged, never retained: the value lands in the
		// committed binding's identity only.
		entry.staged.identity += ":" + hash(input.Values["token"])
		r.commitLocked(entry)
	case "approval":
		if entry.session.State == botflota.SetupAwaitingInput {
			if input.Values["login"] == "" {
				return nil, errors.New("fake: login is required")
			}
			mark()
			entry.session.State = botflota.SetupAwaitingApproval
			entry.session.Step = &botflota.SetupStep{
				Kind:     botflota.SetupStepApproval,
				Approval: &botflota.SetupApproval{Destination: "fake-app", Instruction: "approve-" + entry.session.ID},
			}
			entry.session.Revision++
			entry.session.UpdatedAt = r.now
		} else {
			return nil, botflota.ErrConflict
		}
	case "profile":
		name := input.Values["name"]
		if name == "" {
			name = input.Values["select"]
		}
		if name == "" {
			return nil, errors.New("fake: a profile name is required")
		}
		mark()
		r.backend.mu.Lock()
		if !r.backend.profiles[name] {
			r.backend.profiles[name] = true
		}
		r.backend.selected[entry.session.Bot] = name
		r.backend.mu.Unlock()
		entry.staged.namespace = "profile:" + name
		r.commitLocked(entry)
	default:
		return nil, botflota.ErrConflict
	}
	published := entry.session
	return &published, nil
}

// hash stands in for a credential the fake never retains: only the digest
// reaches the committed identity.
func hash(secret string) string { return fmt.Sprintf("%x", len(secret)*2654435761) }

func (r *fakeRunner) commitLocked(entry *fakeSession) {
	ref := entry.session.Bot
	staged := entry.staged
	if current, ok := r.bindings[ref]; ok &&
		current.provider == staged.provider &&
		current.namespace == staged.namespace &&
		current.identity == staged.identity {
		staged.generation = current.generation
	} else {
		staged.generation = r.generations[ref] + 1
		r.generations[ref] = staged.generation
	}
	committed := *staged
	r.bindings[ref] = &committed
	entry.session.State = botflota.SetupSucceeded
	entry.session.Step = nil
	entry.session.Revision++
	entry.session.UpdatedAt = r.now
	r.activations = append(r.activations, ref)
	r.activated[entry.session.ID]++
	close(entry.closed)
}

// Confirm stages a provider confirmation the way a phone scan or an approval
// callback would: only a live session can commit, and it commits once.
func (r *fakeRunner) Confirm(scope, bot, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[botflota.BotRef{Scope: scope, Bot: bot}]
	if !ok || entry.session.ID != sessionID {
		return botflota.ErrNotFound
	}
	if entry.session.Final() {
		return nil
	}
	switch entry.session.Method {
	case "qr":
		r.commitLocked(entry)
	case "approval":
		if entry.session.State != botflota.SetupAwaitingApproval {
			return botflota.ErrConflict
		}
		r.commitLocked(entry)
	default:
		return botflota.ErrConflict
	}
	return nil
}

func (r *fakeRunner) CancelSetup(
	_ context.Context, scope, bot, sessionID string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.sessions[botflota.BotRef{Scope: scope, Bot: bot}]
	if !ok || entry.session.ID != sessionID {
		return botflota.ErrNotFound
	}
	if entry.session.Final() {
		return nil
	}
	entry.session.State = botflota.SetupCancelled
	entry.session.ErrorCode = "cancelled"
	entry.session.Step = nil
	entry.session.Revision++
	entry.session.UpdatedAt = r.now
	close(entry.closed)
	return nil
}

func (r *fakeRunner) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for _, entry := range r.sessions {
		if entry.session.Final() {
			continue
		}
		entry.session.State = botflota.SetupCancelled
		entry.session.ErrorCode = "cancelled"
		entry.session.Step = nil
		entry.session.Revision++
		close(entry.closed)
	}
}

// fakeLifecycle inspects and detaches through the runner's committed state.
type fakeLifecycle struct{ runner *fakeRunner }

func (l fakeLifecycle) InspectBinding(
	_ context.Context, scope, bot string,
) (botflota.BindingStatus, error) {
	l.runner.mu.Lock()
	defer l.runner.mu.Unlock()
	ref := botflota.BotRef{Scope: scope, Bot: bot}
	stored, ok := l.runner.bindings[ref]
	if !ok {
		return botflota.BindingStatus{
			Condition: botflota.ReadinessUnconfigured,
			Reasons:   []string{"no account linked"},
			Method:    "qr",
		}, nil
	}
	return botflota.BindingStatus{
		Binding: &botflota.Binding{
			Scope: scope, Bot: bot,
			Provider: stored.provider, Adapter: stored.adapter, Mode: stored.mode,
			Namespace:  stored.namespace,
			Identity:   botflota.IdentityKey{Provider: stored.provider, Subject: stored.identity},
			Generation: stored.generation,
		},
		Condition: botflota.ReadinessConfigured,
		Reasons:   []string{"credentials are stored"},
	}, nil
}

func (l fakeLifecycle) DetachBinding(_ context.Context, scope, bot string) error {
	l.runner.mu.Lock()
	defer l.runner.mu.Unlock()
	ref := botflota.BotRef{Scope: scope, Bot: bot}
	if entry, ok := l.runner.sessions[ref]; ok && !entry.session.Final() {
		entry.session.State = botflota.SetupCancelled
		entry.session.ErrorCode = "cancelled"
		entry.session.Step = nil
		entry.session.Revision++
		close(entry.closed)
	}
	delete(l.runner.bindings, ref)
	return nil
}

func begin(t *testing.T, r *fakeRunner, scope, bot, method, key string) *botflota.SetupSession {
	t.Helper()
	session, err := r.BeginSetup(context.Background(), scope, bot, botflota.SetupBegin{
		Method: method, RequestKey: key,
	})
	if err != nil {
		t.Fatalf("BeginSetup(%s): %v", method, err)
	}
	return session
}

func TestSetupFakesCompleteEveryMethod(t *testing.T) {
	ctx := context.Background()
	r := newFakeRunner()
	lifecycle := fakeLifecycle{r}

	methods, err := r.SetupMethods(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("SetupMethods: %v", err)
	}
	if len(methods) != 4 {
		t.Fatalf("methods = %v, want the four modeled kinds", methods)
	}

	// QR: a display challenge with no secret input; the provider confirms.
	qr := begin(t, r, "dev", "qr-bot", "qr", "key-qr")
	if qr.Step == nil || qr.Step.Kind != botflota.SetupStepDisplay ||
		qr.Step.Display == nil || qr.Step.Display.Format != "qr" ||
		qr.Step.Display.Payload == "" {
		t.Fatalf("qr step = %#v, want a display challenge", qr.Step)
	}
	if qr.State != botflota.SetupAwaitingInput {
		t.Fatalf("qr state = %s, want awaiting_input", qr.State)
	}
	if err := r.Confirm("dev", "qr-bot", qr.ID); err != nil {
		t.Fatalf("Confirm qr: %v", err)
	}
	// Credential-only: one write-only input commits.
	cred := begin(t, r, "dev", "cred-bot", "token", "key-token")
	if len(cred.Step.Fields) != 1 ||
		cred.Step.Fields[0].Kind != botflota.SetupFieldCredential {
		t.Fatalf("token step = %#v, want one credential field", cred.Step)
	}
	after, err := r.InputSetup(ctx, "dev", "cred-bot", cred.ID, botflota.SetupInput{
		Revision: cred.Revision, RequestKey: "input-1",
		Values: map[string]string{"token": "s3cret"},
	})
	if err != nil {
		t.Fatalf("InputSetup token: %v", err)
	}
	if after.State != botflota.SetupSucceeded || after.Step != nil {
		t.Fatalf("token session = %#v, want a committed close", after)
	}
	// The submitted secret never reads back: the step is gone and the
	// session carries no value for it.
	read, err := r.ReadSetup(ctx, "dev", "cred-bot", cred.ID)
	if err != nil {
		t.Fatalf("ReadSetup: %v", err)
	}
	if read.Step != nil {
		t.Fatalf("committed session still carries a step: %#v", read.Step)
	}

	// Multi-step approval: identifier input, then an external approval, then
	// the provider event commits.
	approval := begin(t, r, "dev", "appr-bot", "approval", "key-approval")
	step1, err := r.InputSetup(ctx, "dev", "appr-bot", approval.ID, botflota.SetupInput{
		Revision: approval.Revision, RequestKey: "input-1",
		Values: map[string]string{"login": "owner"},
	})
	if err != nil {
		t.Fatalf("InputSetup login: %v", err)
	}
	if step1.State != botflota.SetupAwaitingApproval ||
		step1.Step.Kind != botflota.SetupStepApproval {
		t.Fatalf("approval session = %#v, want an external approval wait", step1)
	}
	if err := r.Confirm("dev", "appr-bot", approval.ID); err != nil {
		t.Fatalf("Confirm approval: %v", err)
	}

	// Local profile: select or create a managed profile with bounded params.
	profile := begin(t, r, "dev", "prof-bot", "profile", "key-profile")
	if profile.Step.Kind != botflota.SetupStepProfile ||
		profile.Step.Profile == nil || !profile.Step.Profile.AllowCreate {
		t.Fatalf("profile step = %#v, want a managed-profile selection", profile.Step)
	}
	profDone, err := r.InputSetup(ctx, "dev", "prof-bot", profile.ID, botflota.SetupInput{
		Revision: profile.Revision, RequestKey: "input-1",
		Values: map[string]string{"name": "second"},
	})
	if err != nil {
		t.Fatalf("InputSetup profile: %v", err)
	}
	if profDone.State != botflota.SetupSucceeded {
		t.Fatalf("profile session = %#v, want success", profDone)
	}

	// Every committed bot inspects as configured with a generation; the
	// unconfigured one stays setup-capable.
	for bot, want := range map[string]int64{
		"qr-bot": 1, "cred-bot": 1, "appr-bot": 1, "prof-bot": 1,
	} {
		status, err := lifecycle.InspectBinding(ctx, "dev", bot)
		if err != nil {
			t.Fatalf("InspectBinding(%s): %v", bot, err)
		}
		if status.Condition != botflota.ReadinessConfigured ||
			status.Binding == nil || status.Binding.Generation != want {
			t.Fatalf("status(%s) = %#v, want configured at generation %d",
				bot, status, want)
		}
		if status.Binding.Identity.Provider == "" ||
			status.Binding.Identity.Subject == "" {
			t.Fatalf("status(%s) carries no safe identity: %#v", bot, status.Binding)
		}
	}
	fresh, err := lifecycle.InspectBinding(ctx, "dev", "nobody")
	if err != nil {
		t.Fatalf("InspectBinding(nobody): %v", err)
	}
	if fresh.Condition != botflota.ReadinessUnconfigured || fresh.Binding != nil ||
		fresh.Method == "" {
		t.Fatalf("fresh status = %#v, want unconfigured with a method", fresh)
	}

	// No phone field and no universal QR anywhere in the neutral shapes.
	for _, session := range []*botflota.SetupSession{qr, cred, approval, profile} {
		if session.Method == "qr" {
			continue
		}
		if session.Step != nil && session.Step.Display != nil {
			t.Fatalf("method %q carries a display step: universal QR", session.Method)
		}
	}
}

func TestSetupRequestKeysAndStaleRevisions(t *testing.T) {
	ctx := context.Background()
	r := newFakeRunner()

	first := begin(t, r, "dev", "alpha", "token", "start-1")
	again, err := r.BeginSetup(ctx, "dev", "alpha", botflota.SetupBegin{
		Method: "token", RequestKey: "start-1",
	})
	if err != nil {
		t.Fatalf("BeginSetup same key: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("same key returned session %s, want %s", again.ID, first.ID)
	}
	if _, err := r.BeginSetup(ctx, "dev", "alpha", botflota.SetupBegin{
		Method: "approval", RequestKey: "start-1",
	}); !errors.Is(err, botflota.ErrConflict) {
		t.Fatalf("reused key with other params = %v, want ErrConflict", err)
	}
	if _, err := r.BeginSetup(ctx, "dev", "alpha", botflota.SetupBegin{
		Method: "token", RequestKey: "start-2",
	}); !errors.Is(err, botflota.ErrBusy) {
		t.Fatalf("second live session = %v, want ErrBusy", err)
	}

	if _, err := r.ReadSetup(ctx, "dev", "alpha", "no-such-session"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("ReadSetup stale id = %v, want ErrNotFound", err)
	}
	if err := r.CancelSetup(ctx, "dev", "alpha", "no-such-session"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("CancelSetup stale id = %v, want ErrNotFound", err)
	}
	if _, err := r.InputSetup(ctx, "dev", "alpha", "no-such-session",
		botflota.SetupInput{Revision: 1}); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("InputSetup stale id = %v, want ErrNotFound", err)
	}
	if _, err := r.InputSetup(ctx, "dev", "alpha", first.ID,
		botflota.SetupInput{Revision: first.Revision + 10}); !errors.Is(
		err, botflota.ErrConflict,
	) {
		t.Fatalf("stale revision input succeeded, want ErrConflict")
	}

	// An input request key never submits twice: the retry returns the
	// recorded outcome with no second activation.
	done, err := r.InputSetup(ctx, "dev", "alpha", first.ID, botflota.SetupInput{
		Revision: first.Revision, RequestKey: "input-1",
		Values: map[string]string{"token": "s3cret"},
	})
	if err != nil {
		t.Fatalf("InputSetup: %v", err)
	}
	retry, err := r.InputSetup(ctx, "dev", "alpha", first.ID, botflota.SetupInput{
		Revision: done.Revision, RequestKey: "input-1",
		Values: map[string]string{"token": "s3cret"},
	})
	if err != nil {
		t.Fatalf("InputSetup retry: %v", err)
	}
	if retry.State != botflota.SetupSucceeded {
		t.Fatalf("retry state = %s, want the recorded success", retry.State)
	}
	if got := r.activated[first.ID]; got != 1 {
		t.Fatalf("activations = %d, want exactly one", got)
	}
}

func TestSetupCancelVersusCommit(t *testing.T) {
	ctx := context.Background()

	// A cancelled uncommitted session can never activate later: the
	// provider confirmation finds no live session to commit.
	r := newFakeRunner()
	qr := begin(t, r, "dev", "alpha", "qr", "key-1")
	if err := r.CancelSetup(ctx, "dev", "alpha", qr.ID); err != nil {
		t.Fatalf("CancelSetup: %v", err)
	}
	if err := r.Confirm("dev", "alpha", qr.ID); err != nil {
		t.Fatalf("Confirm after cancel: %v", err)
	}
	read, err := r.ReadSetup(ctx, "dev", "alpha", qr.ID)
	if err != nil {
		t.Fatalf("ReadSetup: %v", err)
	}
	if read.State != botflota.SetupCancelled {
		t.Fatalf("state = %s, want the cancellation to stand", read.State)
	}
	if len(r.activations) != 0 {
		t.Fatalf("activations = %v, want none", r.activations)
	}

	// Once committed, cancellation cannot claim the binding never existed:
	// it reports the success that already happened.
	r2 := newFakeRunner()
	qr2 := begin(t, r2, "dev", "alpha", "qr", "key-1")
	if err := r2.Confirm("dev", "alpha", qr2.ID); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := r2.CancelSetup(ctx, "dev", "alpha", qr2.ID); err != nil {
		t.Fatalf("CancelSetup after commit: %v", err)
	}
	kept, err := r2.ReadSetup(ctx, "dev", "alpha", qr2.ID)
	if err != nil {
		t.Fatalf("ReadSetup: %v", err)
	}
	if kept.State != botflota.SetupSucceeded {
		t.Fatalf("state = %s, want the committed success kept", kept.State)
	}
	if got := r2.activated[qr2.ID]; got != 1 {
		t.Fatalf("activations = %d, want exactly one", got)
	}

	// Shutdown cancels every live session and no worker activates later.
	r3 := newFakeRunner()
	live := begin(t, r3, "dev", "alpha", "qr", "key-1")
	r3.Close()
	after, err := r3.ReadSetup(ctx, "dev", "alpha", live.ID)
	if err != nil {
		t.Fatalf("ReadSetup: %v", err)
	}
	if after.State != botflota.SetupCancelled {
		t.Fatalf("state = %s, want shutdown to cancel the live session", after.State)
	}
	if err := r3.Confirm("dev", "alpha", live.ID); err != nil {
		t.Fatalf("Confirm after shutdown: %v", err)
	}
	if len(r3.activations) != 0 {
		t.Fatalf("activations = %v, want none after shutdown", r3.activations)
	}
}

func TestSetupReauthAndReplacement(t *testing.T) {
	ctx := context.Background()
	r := newFakeRunner()
	lifecycle := fakeLifecycle{r}

	// Same-identity reauthorization keeps its generation; the history the
	// test recorded stays with the bot.
	qr := begin(t, r, "dev", "alpha", "qr", "key-1")
	if err := r.Confirm("dev", "alpha", qr.ID); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	r.history[botflota.BotRef{Scope: "dev", Bot: "alpha"}] = 7
	reauth := begin(t, r, "dev", "alpha", "qr", "key-2")
	if err := r.Confirm("dev", "alpha", reauth.ID); err != nil {
		t.Fatalf("Confirm reauth: %v", err)
	}
	status, err := lifecycle.InspectBinding(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("InspectBinding: %v", err)
	}
	if status.Binding.Generation != 1 {
		t.Fatalf("generation = %d, want the renewal to keep 1", status.Binding.Generation)
	}
	if r.history[botflota.BotRef{Scope: "dev", Bot: "alpha"}] != 7 {
		t.Fatal("reauthorization dropped the bot's history")
	}

	// Explicit identity replacement advances the generation; detachment
	// forgets the binding but keeps chats, directory, and history, and the
	// replacement cannot inherit the old queue's generation.
	if err := lifecycle.DetachBinding(ctx, "dev", "alpha"); err != nil {
		t.Fatalf("DetachBinding: %v", err)
	}
	if _, err := lifecycle.InspectBinding(ctx, "dev", "alpha"); err != nil {
		t.Fatalf("InspectBinding after detach: %v", err)
	} else if status, _ := lifecycle.InspectBinding(ctx, "dev", "alpha"); status.Condition != botflota.ReadinessUnconfigured {
		t.Fatalf("status = %#v, want unconfigured after detach", status)
	}
	if r.history[botflota.BotRef{Scope: "dev", Bot: "alpha"}] != 7 {
		t.Fatal("detachment dropped the bot's history")
	}
	r.mu.Lock()
	r.bindings[botflota.BotRef{Scope: "dev", Bot: "alpha"}] = &fakeBinding{
		provider: "fake", adapter: "fake", mode: "token-mode",
		identity: "dev/alpha-replacement",
	}
	r.mu.Unlock()
	next := begin(t, r, "dev", "alpha", "token", "key-3")
	if _, err := r.InputSetup(ctx, "dev", "alpha", next.ID, botflota.SetupInput{
		Revision: next.Revision, Values: map[string]string{"token": "other"},
	}); err != nil {
		t.Fatalf("InputSetup replacement: %v", err)
	}
	replaced, err := lifecycle.InspectBinding(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("InspectBinding: %v", err)
	}
	if replaced.Binding.Generation != 2 {
		t.Fatalf("generation = %d, want the replacement to advance to 2",
			replaced.Binding.Generation)
	}
}

func TestSetupSharedBackendKeepsBindingsIndependent(t *testing.T) {
	ctx := context.Background()
	r := newFakeRunner()

	// Two fixed bindings share one backend: selecting a context for one
	// leaves both identities and generations unchanged, and detaching one
	// cannot destroy the peer's profile.
	first := begin(t, r, "dev", "alpha", "qr", "key-alpha")
	if err := r.Confirm("dev", "alpha", first.ID); err != nil {
		t.Fatalf("Confirm alpha: %v", err)
	}
	second := begin(t, r, "dev", "beta", "profile", "key-beta")
	if _, err := r.InputSetup(ctx, "dev", "beta", second.ID, botflota.SetupInput{
		Revision: second.Revision, Values: map[string]string{"name": "beta-only"},
	}); err != nil {
		t.Fatalf("InputSetup beta: %v", err)
	}
	lifecycle := fakeLifecycle{r}
	alphaBefore, err := lifecycle.InspectBinding(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("InspectBinding alpha: %v", err)
	}
	betaBefore, err := lifecycle.InspectBinding(ctx, "dev", "beta")
	if err != nil {
		t.Fatalf("InspectBinding beta: %v", err)
	}
	r.backend.mu.Lock()
	r.backend.selected[botflota.BotRef{Scope: "dev", Bot: "beta"}] = "beta-only"
	r.backend.mu.Unlock()
	alphaAfter, err := lifecycle.InspectBinding(ctx, "dev", "alpha")
	if err != nil {
		t.Fatalf("InspectBinding alpha: %v", err)
	}
	if *alphaAfter.Binding != *alphaBefore.Binding {
		t.Fatalf("alpha binding moved from %#v to %#v",
			alphaBefore.Binding, alphaAfter.Binding)
	}
	if err := lifecycle.DetachBinding(ctx, "dev", "alpha"); err != nil {
		t.Fatalf("DetachBinding alpha: %v", err)
	}
	betaAfter, err := lifecycle.InspectBinding(ctx, "dev", "beta")
	if err != nil {
		t.Fatalf("InspectBinding beta: %v", err)
	}
	if *betaAfter.Binding != *betaBefore.Binding {
		t.Fatalf("beta binding moved from %#v to %#v",
			betaBefore.Binding, betaAfter.Binding)
	}
	r.backend.mu.Lock()
	defer r.backend.mu.Unlock()
	if !r.backend.profiles["beta-only"] {
		t.Fatal("detaching alpha destroyed the peer's profile")
	}
}
