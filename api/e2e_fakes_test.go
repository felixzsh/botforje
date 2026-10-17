package api

// Test-local fakes for the P4 end-to-end gate: a map-backed binding store
// with the port's generation contract, a lifecycle reading that store, and
// a setup runner speaking one distinct step kind per fake mode. Completing
// a setup recommits the bot's current binding, so reauthorization keeps its
// identity, namespace, and generation.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
)

// e2eBindings is a map-backed binding store with the port's generation
// contract: renewing the same identity space keeps its generation, and an
// unrelated identity advances it (ADR 0053).
type e2eBindings struct {
	mu       sync.Mutex
	accounts map[string]botflota.Binding
}

func (f *e2eBindings) Get(
	_ context.Context, scope, bot string,
) (*botflota.Binding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	account, ok := f.accounts[scope+"/"+bot]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	found := account
	return &found, nil
}

func (f *e2eBindings) List(context.Context) ([]botflota.Binding, error) {
	return nil, nil
}

func (f *e2eBindings) Commit(_ context.Context, account *botflota.Binding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := account.Scope + "/" + account.Bot
	now := time.Now()
	if existing, ok := f.accounts[key]; ok {
		account.CreatedAt = existing.CreatedAt
		if existing.Provider == account.Provider &&
			existing.Namespace == account.Namespace &&
			existing.Identity == account.Identity {
			account.Generation = existing.Generation
		} else {
			account.Generation = existing.Generation + 1
		}
	} else {
		if account.Generation == 0 {
			account.Generation = 1
		}
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	f.accounts[key] = *account
	return nil
}

func (f *e2eBindings) Delete(_ context.Context, scope, bot string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.accounts[scope+"/"+bot]; !ok {
		return botflota.ErrNotFound
	}
	delete(f.accounts, scope+"/"+bot)
	return nil
}

// e2eLifecycle inspects and detaches through the same store the runner
// commits to, so a reauthorization visibly keeps or loses the binding.
type e2eLifecycle struct {
	bindings *e2eBindings
}

func (l *e2eLifecycle) InspectBinding(
	ctx context.Context, scope, bot string,
) (botflota.BindingStatus, error) {
	binding, err := l.bindings.Get(ctx, scope, bot)
	if errors.Is(err, botflota.ErrNotFound) {
		return botflota.BindingStatus{
			Condition: botflota.ReadinessUnconfigured,
		}, nil
	}
	if err != nil {
		return botflota.BindingStatus{}, err
	}
	return botflota.BindingStatus{
		Binding:   binding,
		Condition: botflota.ReadinessConfigured,
	}, nil
}

func (l *e2eLifecycle) DetachBinding(
	ctx context.Context, scope, bot string,
) error {
	return l.bindings.Delete(ctx, scope, bot)
}

// e2eRunner speaks one distinct step kind per fake mode and commits the
// bot's current binding on success: completing a setup reauthorizes the
// same identity instead of inventing a new one.
type e2eRunner struct {
	mu       sync.Mutex
	methods  []string
	bindings *e2eBindings
	sessions map[string]*botflota.SetupSession
	keys     map[string]string
	params   map[string]string
	nextID   int
}

func (r *e2eRunner) SetupMethods(
	_ context.Context, _, _ string,
) ([]string, error) {
	return append([]string{}, r.methods...), nil
}

func (r *e2eRunner) BeginSetup(
	_ context.Context, scope, bot string, req botflota.SetupBegin,
) (*botflota.SetupSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.methods, req.Method) {
		return nil, fmt.Errorf("e2e: no such method: %w",
			botflota.ErrUnsupportedKind)
	}
	if id, ok := r.keys[req.RequestKey]; ok && req.RequestKey != "" {
		if r.params[req.RequestKey] != req.Method {
			return nil, botflota.ErrConflict
		}
		return r.sessions[id], nil
	}
	r.nextID++
	now := time.Now()
	session := &botflota.SetupSession{
		Bot:    botflota.BotRef{Scope: scope, Bot: bot},
		ID:     fmt.Sprintf("e2e-setup-%d", r.nextID),
		Method: req.Method, Mode: "native",
		State:     botflota.SetupAwaitingInput,
		Revision:  1,
		StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
		Step: e2eStep(req.Method, now),
	}
	if req.Method == "approval" {
		session.State = botflota.SetupAwaitingApproval
	}
	r.sessions[session.ID] = session
	if req.RequestKey != "" {
		if r.keys == nil {
			r.keys = map[string]string{}
			r.params = map[string]string{}
		}
		r.keys[req.RequestKey] = session.ID
		r.params[req.RequestKey] = req.Method
	}
	return session, nil
}

// e2eStep is the distinct pending step each fake mode opens with: a QR
// display challenge, a typed credential input, an external approval wait,
// or a managed-profile selection (ADR 0053).
func e2eStep(method string, now time.Time) *botflota.SetupStep {
	switch method {
	case "qr":
		return &botflota.SetupStep{
			Kind: botflota.SetupStepDisplay,
			Display: &botflota.SetupDisplay{
				Format: "qr", Payload: "e2e-qr-payload",
				ExpiresAt: now.Add(2 * time.Minute), Hint: "scan me",
			},
		}
	case "approval":
		return &botflota.SetupStep{
			Kind: botflota.SetupStepApproval,
			Approval: &botflota.SetupApproval{
				Destination: "e2e companion",
				Instruction: "approve code 123",
			},
		}
	case "profile":
		return &botflota.SetupStep{
			Kind: botflota.SetupStepProfile,
			Profile: &botflota.SetupProfile{
				Methods: []string{"profile"}, Options: []string{"main"},
			},
		}
	default:
		return &botflota.SetupStep{
			Kind: botflota.SetupStepInput,
			Fields: []botflota.SetupField{
				{Name: "token", Kind: botflota.SetupFieldCredential},
			},
		}
	}
}

func (r *e2eRunner) ReadSetup(
	_ context.Context, _, _, sessionID string,
) (*botflota.SetupSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return session, nil
}

func (r *e2eRunner) InputSetup(
	ctx context.Context, scope, bot, sessionID string, input botflota.SetupInput,
) (*botflota.SetupSession, error) {
	r.mu.Lock()
	session, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return nil, botflota.ErrNotFound
	}
	if input.Revision != session.Revision {
		r.mu.Unlock()
		return nil, botflota.ErrConflict
	}
	if session.Step != nil && session.Step.Kind == botflota.SetupStepProfile &&
		input.Values["profile"] != "main" {
		r.mu.Unlock()
		return nil, fmt.Errorf("e2e: unknown profile: %w",
			botflota.ErrInvalidParam)
	}
	if session.Method == "approval" && session.Revision == 2 &&
		session.Step != nil &&
		session.Step.Kind == botflota.SetupStepInput &&
		input.Values["code"] != "123456" {
		r.mu.Unlock()
		return nil, fmt.Errorf("e2e: wrong verification code: %w",
			botflota.ErrInvalidParam)
	}
	session.Revision++
	session.UpdatedAt = time.Now()
	if session.Method == "approval" && session.Revision == 2 {
		// The owner confirmed externally; the provider now asks for the
		// verification code as a second step instead of succeeding.
		session.State = botflota.SetupAwaitingInput
		session.Step = &botflota.SetupStep{
			Kind: botflota.SetupStepInput,
			Fields: []botflota.SetupField{
				{Name: "code", Kind: botflota.SetupFieldCode},
			},
		}
		r.mu.Unlock()
		return session, nil
	}
	session.State = botflota.SetupSucceeded
	session.Step = nil
	r.mu.Unlock()
	// Success commits the binding: a stored identity is reauthorized
	// with its namespace and generation kept, while an unbound bot
	// establishes its account binding here for the first time (ADR 0053).
	// A changed identity would advance the generation instead.
	current, err := r.bindings.Get(ctx, scope, bot)
	if errors.Is(err, botflota.ErrNotFound) {
		established := &botflota.Binding{
			Scope: scope, Bot: bot,
			Provider: "whatsapp", Adapter: "e2e-fake", Mode: session.Mode,
			Identity: waKey("5215500000000"),
		}
		if err := r.bindings.Commit(ctx, established); err != nil {
			return nil, err
		}
		return session, nil
	}
	if err != nil {
		return nil, err
	}
	renewed := *current
	if err := r.bindings.Commit(ctx, &renewed); err != nil {
		return nil, err
	}
	return session, nil
}

func (r *e2eRunner) CancelSetup(
	_ context.Context, _, _, sessionID string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[sessionID]
	if !ok {
		return botflota.ErrNotFound
	}
	// A committed session keeps its actual outcome instead.
	if session.State != botflota.SetupSucceeded {
		session.State = botflota.SetupCancelled
		session.UpdatedAt = time.Now()
	}
	return nil
}

type e2eSetups struct {
	lifecycle botflota.AccountLifecycle
	runner    botflota.SetupRunner
}

func (s *e2eSetups) SetupFor(
	scope, bot string,
) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
	return s.lifecycle, s.runner, nil
}
