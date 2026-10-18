package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/felixzsh/botflota"
)

// BotPauser stops and starts the bots a history operation must work without.
// *fleet.Fleet satisfies it; a server without one still serves the history
// routes, and the operations run while the bots keep going.
type BotPauser interface {
	StopBot(scope, bot string) error
	// StopBotForUnpair ends an in-flight send and drops the rest of the queue,
	// which is what the source of a migration needs: it must never send what it
	// no longer owns (ADR 0040).
	StopBotForUnpair(scope, bot string) error
	StartBot(scope, bot string) error
}

// handleHistoryDelete removes one bot's history. The caller confirms the target
// by repeating it, so a mistyped or scripted call cannot erase another bot
// (ADR 0042).
func (s *Server) handleHistoryDelete(w http.ResponseWriter, r *http.Request) {
	scope, bot, ok := s.historyTarget(w, r)
	if !ok {
		return
	}
	if confirm := r.URL.Query().Get("confirm"); confirm != scope+"/"+bot {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"confirm must repeat the target as "+scope+"/"+bot)
		return
	}
	if s.history == nil {
		writeError(w, http.StatusNotImplemented, "unsupported_operation",
			"history operations are not available")
		return
	}
	// The bot lock keeps the operation away from pairing, unbinding, and a
	// second history request: the bot stays stopped for all of it.
	unlock := s.lockBot(scope, bot)
	defer unlock()
	resume, err := s.pauseBots(scope, bot, true)
	if err != nil {
		s.fail(w, "stop bot before deleting history", err)
		return
	}
	defer resume()
	counts, err := s.history.DeleteBot(r.Context(), scope, bot)
	if err != nil {
		s.fail(w, "delete history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope": scope, "bot": bot,
		"removed": countsView(counts),
	})
}

// handleHistoryMigrate moves one bot's history to another bot of the same scope.
func (s *Server) handleHistoryMigrate(w http.ResponseWriter, r *http.Request) {
	scope, bot, ok := s.historyTarget(w, r)
	if !ok {
		return
	}
	var body struct {
		To string `json:"to"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.To == "" {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"to is required")
		return
	}
	if body.To == bot {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"a bot cannot migrate its history to itself")
		return
	}
	if _, exists := s.config().Scopes[scope].Bots[body.To]; !exists {
		// The destination is addressed inside the caller's own scope, so an
		// unknown one is unknown, not forbidden.
		writeError(w, http.StatusNotFound, "not_found", "unknown destination bot")
		return
	}
	if s.history == nil {
		writeError(w, http.StatusNotImplemented, "unsupported_operation",
			"history operations are not available")
		return
	}
	// Both locks are taken in one stable order before the proof is read, so
	// a concurrent detach or setup input cannot invalidate it: neither can
	// restart a bot the move is still working on (ADR 0042). The source is
	// stopped with its queue dropped and the destination drained: neither
	// may keep writing the rows the move is re-keying.
	unlock := s.lockBots(
		botflota.BotRef{Scope: scope, Bot: bot},
		botflota.BotRef{Scope: scope, Bot: body.To},
	)
	defer unlock()
	if !s.checkMigrationCompatible(w, r, scope, bot, body.To) {
		return
	}
	resume, err := s.pauseBots(scope, bot, true)
	if err != nil {
		s.fail(w, "stop bot before moving history", err)
		return
	}
	defer resume()
	resumeDestination, err := s.pauseBots(scope, body.To, false)
	if err != nil {
		s.fail(w, "stop destination bot before moving history", err)
		return
	}
	defer resumeDestination()

	counts, err := s.history.MoveBot(r.Context(), scope, bot, body.To, time.Now())
	if err != nil {
		if errors.Is(err, botflota.ErrDestinationNotEmpty) {
			writeError(w, http.StatusConflict, "destination_not_empty",
				"the destination bot already holds history")
			return
		}
		s.fail(w, "move history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope": scope, "bot": bot, "to": body.To,
		"moved": countsView(counts),
	})
}

// checkMigrationCompatible proves the two bots' bindings can share history
// before anything stops or moves: both bindings must be committed, both
// definitions installed, and the adapters' own compatibility hooks must
// accept the pair. A missing binding, an unknown definition, a nil hook, or
// reported reasons refuse the move with 409 incompatible_binding, before any
// mutation. Shared code never parses opaque subjects itself, and a server
// without the installed catalog cannot prove compatibility either (ADR 0042,
// ADR 0051). Like send admission, the check resolves through the installed
// definitions; unlike it, there is no common fallback proof, so an absent
// catalog refuses instead of allowing.
func (s *Server) checkMigrationCompatible(
	w http.ResponseWriter, r *http.Request, scope, from, to string,
) bool {
	if s.channels == nil {
		writeError(w, http.StatusConflict, "incompatible_binding",
			"channel compatibility cannot be proven on this instance")
		return false
	}
	fromBinding, err := s.bindings.Get(r.Context(), scope, from)
	if err != nil {
		if errors.Is(err, botflota.ErrNotFound) {
			writeError(w, http.StatusConflict, "incompatible_binding",
				"the source bot has no committed binding")
			return false
		}
		s.fail(w, "read source binding", err)
		return false
	}
	toBinding, err := s.bindings.Get(r.Context(), scope, to)
	if err != nil {
		if errors.Is(err, botflota.ErrNotFound) {
			writeError(w, http.StatusConflict, "incompatible_binding",
				"the destination bot has no committed binding")
			return false
		}
		s.fail(w, "read destination binding", err)
		return false
	}
	fromDef, _, err := s.channels.Resolve(s.config().Scopes[scope].Bots[from].Channel)
	if err != nil {
		writeError(w, http.StatusConflict, "incompatible_binding",
			"the source bot's channel is not installed")
		return false
	}
	toDef, _, err := s.channels.Resolve(s.config().Scopes[scope].Bots[to].Channel)
	if err != nil {
		writeError(w, http.StatusConflict, "incompatible_binding",
			"the destination bot's channel is not installed")
		return false
	}
	if fromDef.CompatibleBindings == nil || toDef.CompatibleBindings == nil {
		writeError(w, http.StatusConflict, "incompatible_binding",
			"binding compatibility is unknown for that channel")
		return false
	}
	if reasons := fromDef.CompatibleBindings(*fromBinding, *toBinding); len(reasons) > 0 {
		writeError(w, http.StatusConflict, "incompatible_binding", reasons[0])
		return false
	}
	if reasons := toDef.CompatibleBindings(*fromBinding, *toBinding); len(reasons) > 0 {
		writeError(w, http.StatusConflict, "incompatible_binding", reasons[0])
		return false
	}
	return true
}

// historyTarget resolves the scope and the bot both history operations address.
func (s *Server) historyTarget(
	w http.ResponseWriter, r *http.Request,
) (scope, bot string, ok bool) {
	scope, ok = s.scopeParam(w, r)
	if !ok {
		return "", "", false
	}
	bot, ok = s.botPathParam(w, r, scope)
	if !ok {
		return "", "", false
	}
	return scope, bot, true
}

// pauseBots stops one bot for the duration of an operation and returns the
// function that puts it back. A bot that was not running is left alone, and a
// server without a pauser never stops anything. An operation that could not
// stop its bot is refused instead of running beside it (ADR 0042).
func (s *Server) pauseBots(scope, bot string, discard bool) (func(), error) {
	pauser, ok := s.pauser.(BotPauser)
	if !ok {
		return func() {}, nil
	}
	if !s.botRunning(scope, bot) {
		return func() {}, nil
	}
	stop := pauser.StopBot
	if discard {
		stop = pauser.StopBotForUnpair
	}
	if err := stop(scope, bot); err != nil {
		return nil, err
	}
	return func() {
		if err := pauser.StartBot(scope, bot); err != nil {
			// The work is committed; a bot that will not come back is reported,
			// not undone.
			s.log.Error("api history resume failed", "scope", scope, "bot", bot,
				"error", err)
		}
	}, nil
}

// botRunning reports whether the fleet holds a live actor for one bot. A state
// of "stopped" is the fleet's own word for a bot with no actor (ADR 0039).
func (s *Server) botRunning(scope, bot string) bool {
	for _, status := range s.fleet.Status() {
		if status.Scope == scope && status.Bot == bot {
			return status.State != "stopped"
		}
	}
	return false
}

// countsView is what a history operation reports it removed or moved.
func countsView(counts botflota.HistoryCounts) map[string]int64 {
	return map[string]int64{
		"chats": counts.Chats, "graph_runs": counts.GraphRuns,
		"messages": counts.Messages, "directory": counts.Directory,
	}
}
