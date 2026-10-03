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
		writeError(w, http.StatusNotImplemented, "unsupported",
			"history operations are not available")
		return
	}
	resume := s.pauseBots(scope, bot, true)
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
		writeError(w, http.StatusUnprocessableEntity, "invalid_config",
			"to is required")
		return
	}
	if body.To == bot {
		writeError(w, http.StatusUnprocessableEntity, "invalid_config",
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
		writeError(w, http.StatusNotImplemented, "unsupported",
			"history operations are not available")
		return
	}
	// The source is stopped with its queue dropped and the destination drained:
	// neither may keep writing the rows the move is re-keying.
	resume := s.pauseBots(scope, bot, true)
	defer resume()
	resumeDestination := s.pauseBots(scope, body.To, false)
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
// server without a pauser never stops anything.
func (s *Server) pauseBots(scope, bot string, discard bool) func() {
	pauser, ok := s.pauser.(BotPauser)
	if !ok {
		return func() {}
	}
	if !s.botRunning(scope, bot) {
		return func() {}
	}
	stop := pauser.StopBot
	if discard {
		stop = pauser.StopBotForUnpair
	}
	if err := stop(scope, bot); err != nil {
		s.log.Error("api history pause failed", "scope", scope, "bot", bot,
			"error", err)
		return func() {}
	}
	return func() {
		if err := pauser.StartBot(scope, bot); err != nil {
			// The work is committed; a bot that will not come back is reported,
			// not undone.
			s.log.Error("api history resume failed", "scope", scope, "bot", bot,
				"error", err)
		}
	}
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
		"chats": counts.Chats, "sessions": counts.Sessions,
		"messages": counts.Messages, "contacts": counts.AddressBook,
	}
}
