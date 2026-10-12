package whatsapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/felixzsh/botflota"
)

// A session expires if nobody scans in time; storing the account stops the
// window (ADR 0025).
const defaultPairLifetime = 15 * time.Minute

const pairActivationBudget = 30 * time.Second

// defaultSetupRetain bounds how long a completed session stays readable: long
// enough to report its actual outcome across a cancellation or an expiry
// race, never a resumable secret store (ADR 0053).
const defaultSetupRetain = 30 * time.Minute

type PairManagerOptions struct {
	// Build resolves one bot's pairing settings from config.
	Build func(scope, bot string, fullHistory bool) (PairOptions, error)
	// OnPaired starts the freshly paired bot (ADR 0022); its failure is
	// logged, never reported as a pairing failure.
	OnPaired func(ctx context.Context, scope, bot string) error
	Lifetime time.Duration
	// Retain bounds completed-session reads; zero uses the default.
	Retain time.Duration
	Log    *slog.Logger
	Now    func() time.Time
	// ID mints session ids; the default is random hex.
	ID func() string
}

// PairManager runs at most one live setup session per bot and speaks both
// contracts over the same machinery: the neutral session runner
// (Begin/Read/Input/Cancel) and the WhatsApp pairing translation the CLI and
// the API still use (ADR 0025, ADR 0053). The QR payload never reaches a
// log: callers read it from the session.
type PairManager struct {
	opts PairManagerOptions
	// pair is a field so a test can drive a session without a phone.
	pair     func(context.Context, PairOptions) (PairResult, error)
	mu       sync.Mutex
	sessions map[botflota.BotRef]*pairingSession
	// closed stops new sessions.
	closed bool
	// botMu guards the per-bot locks: one exclusion per bot covers begin
	// and detach together, so a new session can never slip between a
	// detach's wait and its removal (ADR 0053). Bots are config-bounded,
	// so the entries live as long as the manager.
	botMu    sync.Mutex
	botLocks map[botflota.BotRef]*sync.Mutex
}

// botLock returns one bot's setup exclusion. Callers hold it across the
// map mutation that starts a session and across the wait plus removal
// that detaches one; workers never need it, so ending a live session
// cannot deadlock. Lock order is always bot lock before mu.
func (m *PairManager) botLock(ref botflota.BotRef) *sync.Mutex {
	m.botMu.Lock()
	defer m.botMu.Unlock()
	if m.botLocks == nil {
		m.botLocks = make(map[botflota.BotRef]*sync.Mutex)
	}
	lock, ok := m.botLocks[ref]
	if !ok {
		lock = &sync.Mutex{}
		m.botLocks[ref] = lock
	}
	return lock
}

// Detach ends one bot's in-flight setup and runs remove while no new
// session for the bot can start: the wait and the removal hold the same
// exclusion a begin needs. A nil manager holds no session, so remove runs
// directly. The context bounds the setup wait; the removal always runs
// once the wait is over.
func (m *PairManager) Detach(
	ctx context.Context, scope, bot string, remove func(context.Context) error,
) error {
	if m == nil {
		return remove(ctx)
	}
	ref := botflota.BotRef{Scope: scope, Bot: bot}
	lock := m.botLock(ref)
	lock.Lock()
	defer lock.Unlock()
	if err := m.EndPair(ctx, scope, bot); err != nil {
		return fmt.Errorf("whatsapp: end setup before detach: %w", err)
	}
	return remove(ctx)
}

// errPairingClosed reports StartPair after Close.
var errPairingClosed = errors.New("whatsapp: pairing manager is closed")

type pairingSession struct {
	// neutral is the source of truth; the legacy pairing view is projected
	// from it. Guarded by PairManager.mu.
	neutral botflota.SetupSession
	// code is the latest display challenge; each rotation bumps the
	// revision, so a reader can tell a fresh payload from session expiry.
	code string
	// result is the committed outcome, set once the account is stored.
	result botflota.PairingResult
	// legacyError is the full failure text the pairing translation keeps;
	// the neutral view carries only the sanitized ErrorCode.
	legacyError string
	// requestKey is the start idempotency key that created this session.
	requestKey string
	// fullHistory asks for the full history window at registration when the
	// provider syncs one (ADR 0035); it is part of the start parameters, so
	// a retried key with a different window is a conflicting request.
	fullHistory bool
	// inputKeys records the input request keys already answered with the
	// revision they produced, so a retried key returns the outcome instead
	// of submitting a credential or code twice.
	inputKeys map[string]int64
	ctx       context.Context
	cancel    context.CancelFunc
	timer     *time.Timer
	// ended is a final state chosen by Cancel or expiry before the pairing
	// returned; finish must not overwrite it.
	ended botflota.SetupState
	// saved marks the account stored: the pairing really happened, so a
	// cancel or an expiry arriving before success is reported must not
	// rewrite the outcome (ADR 0025).
	saved bool
	// teardown marks an unpair ending this session: the wait stops, but the
	// bot is not activated — the link goes away right after (ADR 0021).
	teardown bool
	// closed signals the session goroutine finished, so one unpair waits
	// for its own session without waiting for other bots' sessions.
	closed chan struct{}
}

// NewPairManager returns nil when Build is missing, so a caller can treat
// pairing as unavailable.
func NewPairManager(opts PairManagerOptions) *PairManager {
	if opts.Build == nil {
		return nil
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = botflota.DiscardLogger()
	}
	if opts.Lifetime <= 0 {
		opts.Lifetime = defaultPairLifetime
	}
	if opts.Retain <= 0 {
		opts.Retain = defaultSetupRetain
	}
	if opts.ID == nil {
		opts.ID = randomSessionID
	}
	return &PairManager{
		opts: opts, pair: Pair, sessions: make(map[botflota.BotRef]*pairingSession),
	}
}

func randomSessionID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("whatsapp: random session id: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

// SetupMethods names the setup methods this installation supports for one
// bot (ADR 0053).
func (m *PairManager) SetupMethods(
	ctx context.Context, scope, bot string,
) ([]string, error) {
	if m == nil {
		return nil, errors.New("whatsapp: no pairing manager")
	}
	if _, err := m.opts.Build(scope, bot, false); err != nil {
		return nil, err
	}
	return []string{SetupMethodQR}, nil
}

// BeginSetup stages one bot's QR setup until the provider confirms it and the
// local binding commits (ADR 0053). A repeated request key returns the same
// session; reusing one for different parameters is rejected.
func (m *PairManager) BeginSetup(
	ctx context.Context, scope, bot string, req botflota.SetupBegin,
) (*botflota.SetupSession, error) {
	if m == nil {
		return nil, errors.New("whatsapp: no pairing manager")
	}
	if req.Method != "" && req.Method != SetupMethodQR {
		return nil, fmt.Errorf("whatsapp: unsupported setup method %q: %w",
			req.Method, botflota.ErrUnsupportedKind)
	}
	fullHistory := false
	for name, value := range req.Params {
		if name != "full_history" {
			return nil, fmt.Errorf("whatsapp: qr setup takes no parameter %q: %w",
				name, botflota.ErrUnsupportedKind)
		}
		switch value {
		case "true":
			fullHistory = true
		case "false":
			fullHistory = false
		default:
			return nil, fmt.Errorf(
				"whatsapp: full_history must be \"true\" or \"false\": %w",
				botflota.ErrInvalidParam)
		}
	}
	key := req.RequestKey
	if key == "" {
		key = "pair-" + m.opts.ID()
	}
	opts, err := m.opts.Build(scope, bot, false)
	if err != nil {
		return nil, err
	}
	botRef := botflota.BotRef{Scope: scope, Bot: bot}
	// The exclusion comes before any clock read: a begin that waits
	// behind a detach must start with a fresh window, never with an
	// expiry computed before the wait and a timer that already fired.
	botLock := m.botLock(botRef)
	botLock.Lock()
	defer botLock.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errPairingClosed
	}
	now := m.opts.Now().UTC()
	m.sweepLocked(now)
	if current, ok := m.sessions[botRef]; ok {
		if current.requestKey == key {
			m.mu.Unlock()
			if current.sameBegin(req) {
				m.mu.Lock()
				published := m.publishLocked(current)
				m.mu.Unlock()
				return published, nil
			}
			return nil, botflota.ErrConflict
		}
		if !vacant(current) {
			m.mu.Unlock()
			return nil, botflota.ErrBusy
		}
	}
	// The begin exclusion also covers detach: no new session slips
	// between a detach's wait and its removal. The session, its times,
	// and its expiry timer are created and registered together under
	// the same lock, before the worker launches.
	now = m.opts.Now().UTC()
	pairCtx, cancel := context.WithCancel(context.Background())
	session := &pairingSession{
		neutral: botflota.SetupSession{
			Bot: botRef, ID: m.opts.ID(), Method: SetupMethodQR, Mode: AccountMode,
			State:     botflota.SetupStarting,
			Revision:  1,
			StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(m.opts.Lifetime),
		},
		requestKey:  key,
		fullHistory: fullHistory,
		inputKeys:   make(map[string]int64),
		ctx:         pairCtx, cancel: cancel, closed: make(chan struct{}),
	}
	m.sessions[botRef] = session
	session.timer = time.AfterFunc(m.opts.Lifetime, func() {
		m.expire(botRef, session)
	})
	// Publish before releasing the same lock Close uses to set m.closed;
	// Close waits this session even before its goroutine is launched.
	published := m.publishLocked(session)
	m.mu.Unlock()

	pairOpts := opts
	pairOpts.FullHistory = fullHistory
	m.launch(botRef, session, pairOpts)
	return published, nil
}

// vacant reports whether a session slot is free for a replacement: the
// outcome is final and its worker finished, so no stale writer can report
// into the session that follows.
func vacant(session *pairingSession) bool {
	if !session.neutral.Final() {
		return false
	}
	select {
	case <-session.closed:
		return true
	default:
		return false
	}
}

// sameBegin reports whether a retried start asks for the same session.
func (s *pairingSession) sameBegin(req botflota.SetupBegin) bool {
	method := req.Method
	if method == "" {
		method = SetupMethodQR
	}
	wantHistory := req.Params["full_history"] == "true"
	return s.neutral.Method == method && s.fullHistory == wantHistory
}

// ReadSetup returns one bot's session by id. A stale or foreign id answers
// ErrNotFound and stays inert: it can never affect a replacement (ADR 0053).
func (m *PairManager) ReadSetup(
	ctx context.Context, scope, bot, sessionID string,
) (*botflota.SetupSession, error) {
	if m == nil {
		return nil, errors.New("whatsapp: no pairing manager")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[botflota.BotRef{Scope: scope, Bot: bot}]
	if !ok || session.neutral.ID != sessionID {
		return nil, botflota.ErrNotFound
	}
	return m.publishLocked(session), nil
}

// InputSetup answers one bot's pending step. The revision must match the
// session's current one; a stale one is rejected without effect. QR setup
// takes no inputs: a well-formed input is still refused, and a retried
// request key returns the recorded outcome instead of submitting twice.
func (m *PairManager) InputSetup(
	ctx context.Context, scope, bot, sessionID string, input botflota.SetupInput,
) (*botflota.SetupSession, error) {
	if m == nil {
		return nil, errors.New("whatsapp: no pairing manager")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[botflota.BotRef{Scope: scope, Bot: bot}]
	if !ok || session.neutral.ID != sessionID {
		return nil, botflota.ErrNotFound
	}
	if input.RequestKey != "" {
		if revision, seen := session.inputKeys[input.RequestKey]; seen {
			if revision == session.neutral.Revision {
				return m.publishLocked(session), nil
			}
			return nil, botflota.ErrConflict
		}
	}
	if input.Revision != session.neutral.Revision {
		return nil, botflota.ErrConflict
	}
	if !session.neutral.Final() {
		return nil, botflota.ErrConflict
	}
	if input.RequestKey != "" {
		session.inputKeys[input.RequestKey] = session.neutral.Revision
	}
	return m.publishLocked(session), nil
}

// CancelSetup ends one bot's session by id. Cancellation of a committed
// session cannot claim the binding never existed: it reports the success
// that already happened. An unknown id is inert.
func (m *PairManager) CancelSetup(
	ctx context.Context, scope, bot, sessionID string,
) error {
	if m == nil {
		return errors.New("whatsapp: no pairing manager")
	}
	key := botflota.BotRef{Scope: scope, Bot: bot}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[key]
	if !ok || session.neutral.ID != sessionID {
		return botflota.ErrNotFound
	}
	if session.neutral.Final() {
		return nil
	}
	// A saved setup is not cancellable; endLocked ignores it and finish
	// reports the success that already happened.
	m.endLocked(session, botflota.SetupCancelled)
	return nil
}

// publishLocked copies the session for its reader, deriving the pending
// display step from the latest challenge. The payload rotates: each new code
// bumps the revision, so rotation is observable separately from the session
// deadline (ADR 0053).
func (m *PairManager) publishLocked(session *pairingSession) *botflota.SetupSession {
	published := session.neutral
	if session.code != "" && !session.neutral.Final() {
		published.Step = &botflota.SetupStep{
			Kind: botflota.SetupStepDisplay,
			Display: &botflota.SetupDisplay{
				Format:  "qr",
				Payload: session.code,
				Hint:    "Scan with WhatsApp > Linked devices. The code refreshes on its own.",
			},
		}
	}
	return &published
}

// sweepLocked forgets retained sessions older than the bound, so completed
// outcomes stay readable across a race without becoming resumable storage.
func (m *PairManager) sweepLocked(now time.Time) {
	for key, session := range m.sessions {
		select {
		case <-session.closed:
		default:
			continue
		}
		if !session.neutral.Final() {
			continue
		}
		if now.Sub(session.neutral.UpdatedAt) < m.opts.Retain {
			continue
		}
		delete(m.sessions, key)
	}
}

func (m *PairManager) bumpLocked(session *pairingSession) {
	session.neutral.Revision++
	session.neutral.UpdatedAt = m.nowLocked()
}

func (m *PairManager) setStateLocked(
	session *pairingSession, state botflota.SetupState, errorCode string,
) {
	session.neutral.State = state
	session.neutral.ErrorCode = errorCode
	if state.Final() {
		session.code = ""
	}
	m.bumpLocked(session)
}

func legacyState(state botflota.SetupState) botflota.PairState {
	switch state {
	case botflota.SetupStarting:
		return botflota.PairPending
	case botflota.SetupAwaitingInput, botflota.SetupAwaitingApproval:
		return botflota.PairWaiting
	case botflota.SetupSucceeded:
		return botflota.PairSucceeded
	case botflota.SetupFailed:
		return botflota.PairFailed
	case botflota.SetupCancelled:
		return botflota.PairCancelled
	case botflota.SetupExpired:
		return botflota.PairExpired
	}
	return botflota.PairPending
}

// legacyOf projects the neutral session onto the pairing shape the CLI and
// the API still read (ADR 0053): the same outcome, never a second engine.
func (m *PairManager) legacyOf(session *pairingSession) *botflota.PairingSession {
	state := legacyState(session.neutral.State)
	projected := &botflota.PairingSession{
		Scope: session.neutral.Bot.Scope, Bot: session.neutral.Bot.Bot,
		Method: SetupMethodQR, State: state,
		StartedAt: session.neutral.StartedAt, UpdatedAt: session.neutral.UpdatedAt,
		ExpiresAt:       session.neutral.ExpiresAt,
		Error:           session.legacyError,
		Result:          session.result,
		ActivationError: session.neutral.ActivationError,
	}
	if !session.neutral.Final() {
		projected.Code = session.code
	}
	return projected
}

func (m *PairManager) StartPair(
	ctx context.Context, scope, bot string, req botflota.PairRequest,
) (*botflota.PairingSession, error) {
	if m == nil {
		return nil, errors.New("whatsapp: no pairing manager")
	}
	opts, err := m.opts.Build(scope, bot, req.FullHistory)
	if err != nil {
		return nil, err
	}
	key := botflota.BotRef{Scope: scope, Bot: bot}
	// As in BeginSetup, the exclusion comes before any clock read, and
	// the session, its times, and its expiry timer are created and
	// registered together under the same lock.
	botLock := m.botLock(key)
	botLock.Lock()
	defer botLock.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errPairingClosed
	}
	if current, ok := m.sessions[key]; ok && !vacant(current) {
		m.mu.Unlock()
		return nil, botflota.ErrBusy
	}
	now := m.opts.Now().UTC()
	pairCtx, cancel := context.WithCancel(context.Background())
	session := &pairingSession{
		neutral: botflota.SetupSession{
			Bot: key, ID: m.opts.ID(), Method: SetupMethodQR, Mode: AccountMode,
			State:     botflota.SetupStarting,
			Revision:  1,
			StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(m.opts.Lifetime),
		},
		requestKey:  "pair-" + m.opts.ID(),
		fullHistory: req.FullHistory,
		inputKeys:   make(map[string]int64),
		ctx:         pairCtx, cancel: cancel, closed: make(chan struct{}),
	}
	m.sessions[key] = session
	session.timer = time.AfterFunc(m.opts.Lifetime, func() {
		m.expire(key, session)
	})
	// Publish before releasing the same lock Close uses to set m.closed;
	// Close waits this session even before its goroutine is launched.
	projected := m.legacyOf(session)
	m.mu.Unlock()

	m.launch(key, session, opts)
	return projected, nil
}

func (m *PairManager) launch(
	key botflota.BotRef, session *pairingSession, opts PairOptions,
) {
	opts.RenderQR = func(code string) { m.setCode(key, code) }
	opts.OnSaved = func() { m.markSaved(key, session) }
	go func() {
		defer close(session.closed)
		result, err := m.pair(session.ctx, opts)
		m.finish(key, session, result, err)
	}()
}

// Close stops accepting sessions, cancels every live one, and waits for the
// pairing goroutines, so no writer touches the stores after their owner
// closes them. A pairing already saved still reports success.
func (m *PairManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closed = true
	closed := make([]<-chan struct{}, 0, len(m.sessions))
	for _, session := range m.sessions {
		closed = append(closed, session.closed)
		if !session.neutral.Final() {
			m.endLocked(session, botflota.SetupCancelled)
			// A saved pairing keeps its outcome; only its wait has to end.
			session.cancel()
		}
	}
	m.mu.Unlock()
	for _, done := range closed {
		<-done
	}
}

func (m *PairManager) CurrentPair(
	ctx context.Context, scope, bot string,
) (*botflota.PairingSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[botflota.BotRef{Scope: scope, Bot: bot}]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return m.legacyOf(session), nil
}

func (m *PairManager) CancelPair(ctx context.Context, scope, bot string) error {
	key := botflota.BotRef{Scope: scope, Bot: bot}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[key]
	if !ok {
		return botflota.ErrNotFound
	}
	if session.neutral.Final() {
		return nil
	}
	// A saved pairing is not cancellable; endLocked ignores it and finish
	// reports the success that already happened.
	m.endLocked(session, botflota.SetupCancelled)
	return nil
}

// EndPair ends the bot's live session and waits for its goroutine, so an
// unpair never races the pairing that stores the account: a session that has
// not stored it aborts and keeps the cancelled outcome; one that has stored
// it only loses its import wait and still reports the link that exists
// (ADR 0021, ADR 0025). A bot with no live session answers nil, so the
// operation is idempotent. The context bounds only the wait — the end itself
// always applies — so a pairing stuck past the caller's deadline is reported
// instead of holding the unpair forever.
func (m *PairManager) EndPair(ctx context.Context, scope, bot string) error {
	if m == nil {
		return errors.New("whatsapp: no pairing manager")
	}
	key := botflota.BotRef{Scope: scope, Bot: bot}
	m.mu.Lock()
	session, ok := m.sessions[key]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	session.teardown = true
	if !session.neutral.Final() {
		m.endLocked(session, botflota.SetupCancelled)
		if session.saved {
			session.cancel()
		}
	}
	closed := session.closed
	m.mu.Unlock()
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("whatsapp: end pairing: %w", ctx.Err())
	}
}

func (m *PairManager) setCode(key botflota.BotRef, code string) {
	if code == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[key]
	if !ok || session.neutral.Final() {
		return
	}
	session.code = code
	if session.neutral.State == botflota.SetupStarting {
		session.neutral.State = botflota.SetupAwaitingInput
	}
	m.bumpLocked(session)
}

// markSaved records that the account is stored: it stops the expiry timer and
// makes the session immune to a cancel or expiry racing the success report.
func (m *PairManager) markSaved(key botflota.BotRef, session *pairingSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[key] != session {
		return
	}
	session.saved = true
	session.timer.Stop()
}

func (m *PairManager) expire(key botflota.BotRef, session *pairingSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[key] != session || session.neutral.Final() {
		return
	}
	m.endLocked(session, botflota.SetupExpired)
}

func (m *PairManager) endLocked(session *pairingSession, state botflota.SetupState) {
	if session.saved {
		return
	}
	session.code = ""
	session.neutral.State = state
	switch state {
	case botflota.SetupCancelled:
		session.neutral.ErrorCode = "cancelled"
	case botflota.SetupExpired:
		session.neutral.ErrorCode = "expired"
	}
	session.ended = state
	m.bumpLocked(session)
	session.timer.Stop()
	session.cancel()
}

// pairingErrorText is what a pairing session reports to its caller. One kind
// of failure lives in a scope the caller may not see — the identity is
// already linked to another bot — so it reports the reason alone and leaves
// the names to the instance log (ADR 0021).
func pairingErrorText(err error) string {
	if errors.Is(err, botflota.ErrIdentityInUse) {
		return botflota.ErrIdentityInUse.Error()
	}
	return err.Error()
}

// setupErrorCode sanitizes a setup failure for the neutral view: it names
// the failure, never the credential, identity, or provider detail behind it.
func setupErrorCode(err error) string {
	if errors.Is(err, botflota.ErrIdentityInUse) {
		return "identity_in_use"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "expired"
	}
	return "setup_failed"
}

// finish applies what the pairing produced, unless Cancel or expiry already
// decided the outcome. A successful pairing activates the bot before the
// session reports it, so whoever reads "paired" sees a started bot.
func (m *PairManager) finish(
	key botflota.BotRef, session *pairingSession, result PairResult, err error,
) {
	session.timer.Stop()
	m.mu.Lock()
	if m.sessions[key] != session {
		m.mu.Unlock()
		return
	}
	if session.ended != "" && !session.saved {
		// Cancelled or expired before anything durable landed: the
		// outcome stands and the late result is dropped.
		m.mu.Unlock()
		return
	}
	// A commit reached the store even though cancel or expiry won the
	// race first: the durable association exists, so the session reports
	// the success that already happened instead of hiding it.
	teardown := session.teardown
	// An error after the account is stored only ends the wait (shutdown or
	// cancellation); the pairing itself happened and still reports success.
	if err != nil && !session.saved {
		session.neutral.State = botflota.SetupFailed
		session.neutral.ErrorCode = setupErrorCode(err)
		session.legacyError = pairingErrorText(err)
		session.code = ""
		scope, bot := session.neutral.Bot.Scope, session.neutral.Bot.Bot
		m.bumpLocked(session)
		m.mu.Unlock()
		m.opts.Log.Warn("pairing failed", "scope", scope, "bot", bot, "error", err)
		return
	}
	scope, bot := session.neutral.Bot.Scope, session.neutral.Bot.Bot
	// Saved also when Pair reported success without OnSaved (the
	// already-paired shortcut), so a cancel in the window below cannot
	// rewrite the outcome.
	session.saved = true
	// A reconciled race keeps no error text: the cancel or expiry that
	// arrived after the commit describes timing, not the association.
	if session.ended != "" {
		session.ended = ""
		session.neutral.ErrorCode = ""
	}
	interrupted := err
	m.mu.Unlock()
	if interrupted != nil {
		m.opts.Log.Warn("pairing wait interrupted; the account is stored",
			"scope", scope, "bot", bot, "error", interrupted)
	}

	// An unpair ends this wait on purpose and removes the link right after:
	// activating the bot now would only connect it to be stopped again. The
	// session still reports the success that already happened.
	if m.opts.OnPaired != nil && !teardown {
		activation, cancel := context.WithTimeout(context.Background(),
			pairActivationBudget)
		startErr := m.opts.OnPaired(activation, scope, bot)
		cancel()
		if startErr != nil {
			m.opts.Log.Warn("start bot after pairing", "scope", scope,
				"bot", bot, "error", startErr)
			m.mu.Lock()
			if current, ok := m.sessions[key]; ok && current == session {
				// The binding is committed; only its activation failed.
				// Both views report the failure separately from the
				// setup outcome.
				session.neutral.ActivationError = startErr.Error()
				m.bumpLocked(session)
			}
			m.mu.Unlock()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[key] != session || (session.ended != "" && !session.saved) {
		return
	}
	session.neutral.State = botflota.SetupSucceeded
	session.result = pairingResult(result)
	m.bumpLocked(session)
}

func (m *PairManager) nowLocked() time.Time { return m.opts.Now().UTC() }

func pairingResult(result PairResult) botflota.PairingResult {
	return botflota.PairingResult{
		Identity:    result.Identity,
		Chunks:      result.History.Chunks,
		Chats:       result.History.Conversations,
		Messages:    result.History.Imported,
		Duplicate:   result.History.Duplicate,
		Skipped:     result.History.Skipped,
		AddressBook: result.AddressBook,
		Elapsed:     result.Elapsed,
	}
}
