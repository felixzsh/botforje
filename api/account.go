package api

import (
	"errors"
	"net/http"

	"github.com/felixzsh/botflota"
)

// handleAccountDelete drops the bot's link to its provider: it cancels a live
// pairing session so nothing re-saves the account underneath the delete,
// stops the running bot — StopBot waits for the disconnect, because one
// phone keeps one connection (ADR 0021) — and only then removes the
// credentials and the account record (ADR 0039).
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
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
	if err := s.pairing.CancelPair(ctx, scope, bot); err != nil &&
		!errors.Is(err, botflota.ErrNotFound) {
		s.fail(w, "cancel pairing", err)
		return
	}
	// A pairing that saves its account in this exact window survives this
	// delete; the next one catches it.
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
