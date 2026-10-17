package testchannel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
)

// setupLifetime bounds one uncommitted setup session; a restart requires a
// fresh session and persists no secret for resumability (ADR 0053).
const setupLifetime = 5 * time.Minute

// AccountOwner is the neutral account lifecycle of one testchannel
// installation (ADR 0053). It reads and forgets the committed binding and
// never touches the provider account, which lives in memory.
type AccountOwner struct {
	Bindings botflota.BindingStore
	// Runner cancels the bot's in-flight setup before the binding is
	// forgotten, so a stale worker never activates later (ADR 0053).
	Runner *Runner
}

// InspectBinding answers one bot's committed binding with its actionable
// readiness condition. Credential presence, authorization, and transport
// health stay separate facts, so a committed binding reports configured while
// still naming what remains unknown.
func (o AccountOwner) InspectBinding(
	ctx context.Context, scope, bot string,
) (botflota.BindingStatus, error) {
	binding, err := o.Bindings.Get(ctx, scope, bot)
	if errors.Is(err, botflota.ErrNotFound) {
		return botflota.BindingStatus{
			Condition: botflota.ReadinessUnconfigured,
			Reasons:   []string{"no account linked"},
			Method:    SetupMethodCredential,
		}, nil
	}
	if err != nil {
		return botflota.BindingStatus{}, fmt.Errorf("testchannel: read binding: %w", err)
	}
	if binding.Identity.Provider != Provider {
		return botflota.BindingStatus{}, fmt.Errorf(
			"testchannel: bot %q (scope %q) names provider %q",
			bot, scope, binding.Identity.Provider)
	}
	return botflota.BindingStatus{
		Binding:   binding,
		Condition: botflota.ReadinessConfigured,
		Reasons: []string{
			"local credentials are stored",
			"provider authorization and transport health are unknown until connect",
		},
	}, nil
}

// DetachBinding forgets one bot's local binding and cancels its in-flight
// setup. It leaves chats, directory, and history alone and never destroys a
// provider account (ADR 0053).
func (o AccountOwner) DetachBinding(ctx context.Context, scope, bot string) error {
	if o.Runner != nil {
		o.Runner.cancelAll(scope, bot)
	}
	if err := o.Bindings.Delete(ctx, scope, bot); err != nil &&
		!errors.Is(err, botflota.ErrNotFound) {
		return fmt.Errorf("testchannel: delete binding: %w", err)
	}
	return nil
}

// Runner drives one bot's neutral account setup through the credential input
// step: the session machinery is the shared one, and the adapter contributes
// only what its own method means (ADR 0053).
type Runner struct {
	bindings  botflota.BindingStore
	namespace string
	now       func() time.Time

	mu       sync.Mutex
	seq      int64
	sessions map[botflota.BotRef]*setupSession
}

type setupSession struct {
	ref        botflota.BotRef
	id         string
	method     string
	state      botflota.SetupState
	revision   int64
	requestKey string
	params     map[string]string
	inputKey   string
	startedAt  time.Time
	updatedAt  time.Time
	expiresAt  time.Time
	step       *botflota.SetupStep
}

func newRunner(
	bindings botflota.BindingStore, namespace string, now func() time.Time,
) *Runner {
	if now == nil {
		now = time.Now
	}
	return &Runner{
		bindings: bindings, namespace: namespace, now: now,
		sessions: map[botflota.BotRef]*setupSession{},
	}
}

// SetupMethods names the setup methods this installation supports for one bot.
func (r *Runner) SetupMethods(
	context.Context, string, string,
) ([]string, error) {
	return []string{SetupMethodCredential}, nil
}

// BeginSetup starts one credential session. RequestKey makes the start
// idempotent: repeating it returns the recorded session, while reusing it for
// different parameters is rejected. One setup mutation per bot runs at a time
// (ADR 0053).
func (r *Runner) BeginSetup(
	ctx context.Context, scope, bot string, req botflota.SetupBegin,
) (*botflota.SetupSession, error) {
	method := req.Method
	if method == "" {
		method = SetupMethodCredential
	}
	if method != SetupMethodCredential {
		return nil, fmt.Errorf(
			"testchannel: setup method %q: %w", method, botflota.ErrInvalidParam)
	}
	ref := botflota.BotRef{Scope: scope, Bot: bot}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.sessions[ref]; existing != nil {
		if req.RequestKey != "" && existing.requestKey == req.RequestKey {
			if !sameParams(existing.params, req.Params) {
				return nil, fmt.Errorf(
					"testchannel: request key reused with different parameters: %w",
					botflota.ErrConflict)
			}
			return copySession(existing), nil
		}
		if !existing.state.Final() {
			return nil, fmt.Errorf(
				"testchannel: a setup session is already live: %w", botflota.ErrBusy)
		}
	}
	r.seq++
	now := r.now()
	session := &setupSession{
		ref: ref, id: fmt.Sprintf("setup-%d", r.seq), method: method,
		state: botflota.SetupAwaitingInput, revision: 1,
		requestKey: req.RequestKey, params: cloneParams(req.Params),
		startedAt: now, updatedAt: now, expiresAt: now.Add(setupLifetime),
		step: &botflota.SetupStep{
			Kind: botflota.SetupStepInput,
			Fields: []botflota.SetupField{{
				Name: SetupFieldCredential, Kind: botflota.SetupFieldCredential,
				Label: "Account credential",
			}},
		},
	}
	r.sessions[ref] = session
	return copySession(session), nil
}

// ReadSetup returns one session without its submitted values: a credential is
// write-only (ADR 0053).
func (r *Runner) ReadSetup(
	_ context.Context, scope, bot, sessionID string,
) (*botflota.SetupSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.findLocked(scope, bot, sessionID)
	if session == nil {
		return nil, fmt.Errorf("testchannel: setup session: %w", botflota.ErrNotFound)
	}
	return copySession(session), nil
}

// InputSetup answers the pending credential step, commits the binding, and
// reports the actual outcome. A stale revision or a request key already used
// for different values is rejected without effect (ADR 0053).
func (r *Runner) InputSetup(
	ctx context.Context, scope, bot, sessionID string, input botflota.SetupInput,
) (*botflota.SetupSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.findLocked(scope, bot, sessionID)
	if session == nil {
		return nil, fmt.Errorf("testchannel: setup session: %w", botflota.ErrNotFound)
	}
	if session.inputKey != "" && session.inputKey == input.RequestKey {
		return copySession(session), nil
	}
	if session.state.Final() {
		return nil, fmt.Errorf(
			"testchannel: setup session is final: %w", botflota.ErrConflict)
	}
	if input.Revision != session.revision {
		return nil, fmt.Errorf(
			"testchannel: stale setup revision %d: %w",
			input.Revision, botflota.ErrConflict)
	}
	if session.step == nil || session.step.Kind != botflota.SetupStepInput {
		return nil, fmt.Errorf(
			"testchannel: session awaits no input: %w", botflota.ErrConflict)
	}
	credential := input.Values[SetupFieldCredential]
	if credential == "" {
		return nil, fmt.Errorf(
			"testchannel: credential is required: %w", botflota.ErrInvalidParam)
	}
	binding := &botflota.Binding{
		Scope: scope, Bot: bot,
		Provider: Provider, Adapter: Kind, Mode: Mode,
		Namespace: r.namespace,
		Identity:  botflota.IdentityKey{Provider: Provider, Subject: subjectFor(credential)},
	}
	if err := r.bindings.Commit(ctx, binding); err != nil {
		return nil, fmt.Errorf("testchannel: commit binding: %w", err)
	}
	session.state = botflota.SetupSucceeded
	session.revision++
	session.step = nil
	session.inputKey = input.RequestKey
	session.updatedAt = r.now()
	return copySession(session), nil
}

// CancelSetup cancels one session; a final session is left as it is, so the
// operation is idempotent.
func (r *Runner) CancelSetup(
	_ context.Context, scope, bot, sessionID string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.findLocked(scope, bot, sessionID)
	if session == nil {
		return fmt.Errorf("testchannel: setup session: %w", botflota.ErrNotFound)
	}
	if session.state.Final() {
		return nil
	}
	session.state = botflota.SetupCancelled
	session.revision++
	session.step = nil
	session.updatedAt = r.now()
	return nil
}

// cancelAll marks every live session of one bot cancelled, so detaching a
// binding leaves no worker that could activate it later (ADR 0053).
func (r *Runner) cancelAll(scope, bot string) {
	ref := botflota.BotRef{Scope: scope, Bot: bot}
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[ref]
	if session == nil || session.state.Final() {
		return
	}
	session.state = botflota.SetupCancelled
	session.revision++
	session.step = nil
	session.updatedAt = r.now()
}

func (r *Runner) findLocked(scope, bot, sessionID string) *setupSession {
	session := r.sessions[botflota.BotRef{Scope: scope, Bot: bot}]
	if session == nil || session.id != sessionID {
		return nil
	}
	if session.expiresAt.Before(r.now()) && !session.state.Final() {
		session.state = botflota.SetupExpired
		session.revision++
		session.step = nil
	}
	return session
}

// copySession returns a reader's copy; the step is cloned so a caller cannot
// change what the runner holds, and no submitted value is ever exposed.
func copySession(session *setupSession) *botflota.SetupSession {
	copied := &botflota.SetupSession{
		Bot: session.ref,
		ID:  session.id, Method: session.method, Mode: Mode,
		State: session.state, Revision: session.revision,
		StartedAt: session.startedAt, UpdatedAt: session.updatedAt,
		ExpiresAt: session.expiresAt,
	}
	if session.step != nil {
		step := *session.step
		copied.Step = &step
	}
	if session.state == botflota.SetupFailed {
		copied.ErrorCode = "setup_failed"
	}
	return copied
}

// subjectFor derives a stable, opaque account subject from the submitted
// credential without persisting the credential itself.
func subjectFor(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return "acct-" + hex.EncodeToString(sum[:6])
}

func sameParams(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func cloneParams(params map[string]string) map[string]string {
	if len(params) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	return cloned
}
