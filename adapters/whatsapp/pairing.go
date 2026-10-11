package whatsapp

import (
	"context"
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

type PairManagerOptions struct {
	// Build resolves one bot's pairing settings from config.
	Build func(scope, bot string, fullHistory bool) (PairOptions, error)
	// OnPaired starts the freshly paired bot (ADR 0022); its failure is
	// logged, never reported as a pairing failure.
	OnPaired func(ctx context.Context, scope, bot string) error
	Lifetime time.Duration
	Log      *slog.Logger
	Now      func() time.Time
}

// PairManager runs at most one live pairing session per bot. The QR code
// never reaches a log: callers read it from the session.
type PairManager struct {
	opts PairManagerOptions
	// pair is a field so a test can drive a session without a phone.
	pair     func(context.Context, PairOptions) (PairResult, error)
	mu       sync.Mutex
	sessions map[botflota.BotRef]*pairingSession
	// closed stops new sessions.
	closed bool
}

// errPairingClosed reports StartPair after Close.
var errPairingClosed = errors.New("whatsapp: pairing manager is closed")

type pairingSession struct {
	state  botflota.PairingSession // guarded by PairManager.mu
	ctx    context.Context
	cancel context.CancelFunc
	timer  *time.Timer
	// ended is a final state chosen by Cancel or expiry before the pairing
	// returned; finish must not overwrite it.
	ended botflota.PairState
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
	return &PairManager{
		opts: opts, pair: Pair, sessions: make(map[botflota.BotRef]*pairingSession),
	}
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
	now := m.opts.Now().UTC()
	pairCtx, cancel := context.WithCancel(context.Background())
	key := botflota.BotRef{Scope: scope, Bot: bot}
	session := &pairingSession{
		state: botflota.PairingSession{
			Scope: scope, Bot: bot, Method: "qr",
			State:     botflota.PairPending,
			StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(m.opts.Lifetime),
		},
		ctx: pairCtx, cancel: cancel, closed: make(chan struct{}),
	}
	// The timer exists before the session is visible, so no reader can find
	// a session without one.
	session.timer = time.AfterFunc(m.opts.Lifetime, func() {
		m.expire(key, session)
	})
	published := session.state

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		session.timer.Stop()
		cancel()
		return nil, errPairingClosed
	}
	if current, ok := m.sessions[key]; ok {
		select {
		case <-current.closed:
		default:
			m.mu.Unlock()
			session.timer.Stop()
			cancel()
			return nil, botflota.ErrBusy
		}
	}
	m.sessions[key] = session
	// Publish before releasing the same lock Close uses to set m.closed;
	// Close waits this session even before its goroutine is launched.
	m.mu.Unlock()

	opts.RenderQR = func(code string) { m.setCode(key, code) }
	opts.OnSaved = func() { m.markSaved(key, session) }
	go func() {
		defer close(session.closed)
		result, err := m.pair(pairCtx, opts)
		m.finish(key, session, result, err)
	}()
	return &published, nil
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
		if !session.state.Final() {
			m.endLocked(session, botflota.PairCancelled)
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
	published := session.state
	return &published, nil
}

func (m *PairManager) CancelPair(ctx context.Context, scope, bot string) error {
	key := botflota.BotRef{Scope: scope, Bot: bot}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[key]
	if !ok {
		return botflota.ErrNotFound
	}
	if session.state.Final() {
		return nil
	}
	// A saved pairing is not cancellable; endLocked ignores it and finish
	// reports the success that already happened.
	m.endLocked(session, botflota.PairCancelled)
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
	if !session.state.Final() {
		m.endLocked(session, botflota.PairCancelled)
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
	if !ok || session.state.Final() {
		return
	}
	session.state.Code = code
	if session.state.State == botflota.PairPending {
		session.state.State = botflota.PairWaiting
	}
	session.state.UpdatedAt = m.nowLocked()
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
	if m.sessions[key] != session || session.state.Final() {
		return
	}
	m.endLocked(session, botflota.PairExpired)
}

func (m *PairManager) endLocked(session *pairingSession, state botflota.PairState) {
	if session.saved {
		return
	}
	session.state.State = state
	session.state.Code = ""
	session.state.UpdatedAt = m.nowLocked()
	session.ended = state
	session.timer.Stop()
	session.cancel()
}

// pairingErrorText is what a pairing session reports to its caller. One kind
// of failure lives in a scope the caller may not see — the phone is already
// linked to another bot — so it reports the reason alone and leaves the names
// to the instance log (ADR 0021).
func pairingErrorText(err error) string {
	if errors.Is(err, botflota.ErrIdentityInUse) {
		return botflota.ErrIdentityInUse.Error()
	}
	return err.Error()
}

// finish applies what the pairing produced, unless Cancel or expiry already
// decided the outcome. A successful pairing activates the bot before the
// session reports it, so whoever reads "paired" sees a started bot.
func (m *PairManager) finish(
	key botflota.BotRef, session *pairingSession, result PairResult, err error,
) {
	session.timer.Stop()
	m.mu.Lock()
	if m.sessions[key] != session || session.ended != "" {
		m.mu.Unlock()
		return
	}
	teardown := session.teardown
	state := &session.state
	state.Code = ""
	state.UpdatedAt = m.nowLocked()
	// An error after the account is stored only ends the wait (shutdown or
	// cancellation); the pairing itself happened and still reports success.
	if err != nil && !session.saved {
		state.State = botflota.PairFailed
		state.Error = pairingErrorText(err)
		scope, bot := state.Scope, state.Bot
		m.mu.Unlock()
		m.opts.Log.Warn("pairing failed", "scope", scope, "bot", bot, "error", err)
		return
	}
	scope, bot := state.Scope, state.Bot
	// Saved also when Pair reported success without OnSaved (the
	// already-paired shortcut), so a cancel in the window below cannot
	// rewrite the outcome.
	session.saved = true
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
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[key] != session || session.ended != "" {
		return
	}
	state.State = botflota.PairSucceeded
	state.Result = pairingResult(result)
	state.UpdatedAt = m.nowLocked()
}

func (m *PairManager) nowLocked() time.Time { return m.opts.Now().UTC() }

func pairingResult(result PairResult) botflota.PairingResult {
	return botflota.PairingResult{
		Phone:       result.Identity.Subject,
		Chunks:      result.History.Chunks,
		Chats:       result.History.Conversations,
		Messages:    result.History.Imported,
		Duplicate:   result.History.Duplicate,
		Skipped:     result.History.Skipped,
		AddressBook: result.AddressBook,
		Elapsed:     result.Elapsed,
	}
}
