package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
)

// botView is one configured bot as the API reports it: its identity, its
// stored pairing, and its live state.
type botView struct {
	Scope   string `json:"scope"`
	Bot     string `json:"bot"`
	JID     string `json:"jid,omitempty"`
	Pairing string `json:"pairing,omitempty"`
	State   string `json:"state,omitempty"`
	Outbox  int    `json:"outbox"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	statuses := s.fleet.Status()
	bots := make([]botView, 0, len(statuses))
	for _, status := range statuses {
		bots = append(bots, botView{
			Scope:  status.Scope,
			Bot:    status.Bot,
			State:  status.State,
			Outbox: status.OutboxDepth,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_seconds": int(time.Since(s.started).Seconds()),
		"bots":           bots,
	})
}

func (s *Server) handleBots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	live := make(map[string]botView)
	for _, status := range s.fleet.Status() {
		live[status.Scope+"\x00"+status.Bot] = botView{
			State:  status.State,
			Outbox: status.OutboxDepth,
		}
	}
	bots := make([]botView, 0)
	for _, scope := range config.SortedKeys(s.cfg.Scopes) {
		for _, bot := range config.SortedKeys(s.cfg.Scopes[scope].Bots) {
			view := live[scope+"\x00"+bot]
			view.Scope = scope
			view.Bot = bot
			account, err := s.accounts.Get(ctx, scope, bot)
			switch {
			case err == nil:
				view.JID = account.JID
			case errors.Is(err, botflota.ErrNotFound):
			default:
				s.fail(w, "read linked account", err)
				return
			}
			pairing, err := s.pairings.Get(ctx, scope, bot)
			switch {
			case err == nil:
				view.Pairing = string(pairing.State)
			case errors.Is(err, botflota.ErrNotFound):
			default:
				s.fail(w, "read pairing status", err)
				return
			}
			bots = append(bots, view)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bots": bots})
}

// handleConfig serves the typed model the loader produced, so the Phase 8
// catalogs surface without reworking handlers. Secrets marshal masked
// (ADR 0014).
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}

// fail reports an internal read error and hides its detail from the client.
func (s *Server) fail(w http.ResponseWriter, operation string, err error) {
	s.log.Error("api request failed", "operation", operation, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}
