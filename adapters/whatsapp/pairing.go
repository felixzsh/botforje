package whatsapp

import (
	"context"
	"errors"
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
	sessions map[string]*pairingSession
}

type pairingSession struct {
	state  botflota.PairingSession // guarded by PairManager.mu
	ctx    context.Context
	cancel context.CancelFunc
	timer  *time.Timer
	// ended is a final state chosen by Cancel or expiry before the pairing
	// returned; finish must not overwrite it.
	ended botflota.PairState
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
		opts: opts, pair: Pair, sessions: make(map[string]*pairingSession),
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
	key := scope + "/" + bot
	session := &pairingSession{
		state: botflota.PairingSession{
			Scope: scope, Bot: bot, Method: "qr",
			State:     botflota.PairPending,
			StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(m.opts.Lifetime),
		},
		ctx: pairCtx, cancel: cancel,
	}
	// The timer exists before the session is visible, so no reader can find
	// a session without one.
	session.timer = time.AfterFunc(m.opts.Lifetime, func() {
		m.expire(key, session)
	})
	published := session.state

	m.mu.Lock()
	if current, ok := m.sessions[key]; ok && !current.state.Final() {
		m.mu.Unlock()
		session.timer.Stop()
		cancel()
		return nil, botflota.ErrBusy
	}
	m.sessions[key] = session
	m.mu.Unlock()

	opts.RenderQR = func(code string) { m.setCode(key, code) }
	opts.OnSaved = func() { m.stopTimer(key, session) }
	go func() {
		result, err := m.pair(pairCtx, opts)
		m.finish(key, session, result, err)
	}()
	return &published, nil
}

func (m *PairManager) CurrentPair(
	ctx context.Context, scope, bot string,
) (*botflota.PairingSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[scope+"/"+bot]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	published := session.state
	return &published, nil
}

func (m *PairManager) CancelPair(ctx context.Context, scope, bot string) error {
	key := scope + "/" + bot
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[key]
	if !ok {
		return botflota.ErrNotFound
	}
	if session.state.Final() {
		return nil
	}
	m.endLocked(session, botflota.PairCancelled)
	return nil
}

func (m *PairManager) setCode(key, code string) {
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

func (m *PairManager) stopTimer(key string, session *pairingSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[key] == session {
		session.timer.Stop()
	}
}

func (m *PairManager) expire(key string, session *pairingSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[key] != session || session.state.Final() {
		return
	}
	m.endLocked(session, botflota.PairExpired)
}

func (m *PairManager) endLocked(session *pairingSession, state botflota.PairState) {
	session.state.State = state
	session.state.Code = ""
	session.state.UpdatedAt = m.nowLocked()
	session.ended = state
	session.timer.Stop()
	session.cancel()
}

// finish applies what the pairing produced, unless Cancel or expiry already
// decided the outcome. A successful pairing activates the bot before the
// session reports it, so whoever reads "paired" sees a started bot.
func (m *PairManager) finish(
	key string, session *pairingSession, result PairResult, err error,
) {
	session.timer.Stop()
	m.mu.Lock()
	if m.sessions[key] != session || session.ended != "" {
		m.mu.Unlock()
		return
	}
	state := &session.state
	state.Code = ""
	state.UpdatedAt = m.nowLocked()
	if err != nil {
		state.State = botflota.PairFailed
		state.Error = err.Error()
		scope, bot := state.Scope, state.Bot
		m.mu.Unlock()
		m.opts.Log.Warn("pairing failed", "scope", scope, "bot", bot, "error", err)
		return
	}
	scope, bot := state.Scope, state.Bot
	m.mu.Unlock()

	if m.opts.OnPaired != nil {
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
		Phone:     Number(result.JID),
		Chunks:    result.History.Chunks,
		Chats:     result.History.Conversations,
		Messages:  result.History.Imported,
		Duplicate: result.History.Duplicate,
		Skipped:   result.History.Skipped,
		Contacts:  result.Contacts,
		Elapsed:   result.Elapsed,
	}
}
