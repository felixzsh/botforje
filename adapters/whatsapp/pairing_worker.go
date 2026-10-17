package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/felixzsh/botflota"
)

func (m *PairManager) launch(
	key botflota.BotRef, session *pairingSession, opts PairOptions,
) {
	opts.RenderQR = func(code string) { m.setCode(key, code) }
	opts.OnSaved = func() { m.markSaved(key, session) }
	go func() {
		defer close(session.closed)
		_, err := m.pair(session.ctx, opts)
		m.finish(key, session, err)
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
// session reports it, so whoever reads the success sees a started bot.
func (m *PairManager) finish(
	key botflota.BotRef, session *pairingSession, err error,
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
				// The neutral view reports the failure separately from
				// the setup outcome.
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
	m.bumpLocked(session)
}
