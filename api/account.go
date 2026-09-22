package api

import (
	"errors"
	"net/http"

	"github.com/felixzsh/botflota"
)

// handleAccountDelete drops the bot's link to its provider as one operation
// under the bot's lock: the live pairing session ends and its goroutine
// finishes first — a session that stored the account only loses its import
// wait and still reports that link — then the running bot stops, because
// StopBot waits for the disconnect and one phone keeps one connection (ADR
// 0021), and only then are the credentials and the account record removed
// (ADR 0039).
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	unlock := s.lockBot(scope, bot)
	defer unlock()
	ctx := r.Context()
	if _, err := s.accounts.Get(ctx, scope, bot); err != nil {
		if errors.Is(err, botflota.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				"no linked account for that bot")
			return
		}
		s.fail(w, "read account", err)
		return
	}
	if err := s.pairing.EndPair(ctx, scope, bot); err != nil {
		s.fail(w, "end pairing", err)
		return
	}
	if err := s.fleet.StopBot(scope, bot); err != nil {
		s.fail(w, "stop bot", err)
		return
	}
	if err := s.unpairer.UnpairAccount(ctx, scope, bot); err != nil {
		s.fail(w, "unpair account", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
