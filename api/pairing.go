package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/felixzsh/botflota"
)

type pairingView struct {
	Scope     string    `json:"scope"`
	Bot       string    `json:"bot"`
	Method    string    `json:"method"`
	State     string    `json:"state"`
	Code      string    `json:"code,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Error     string    `json:"error,omitempty"`
	Result    *struct {
		Phone         string  `json:"phone"`
		Chats         int     `json:"chats"`
		Messages      int     `json:"messages"`
		Duplicate     int     `json:"duplicate"`
		Skipped       int     `json:"skipped"`
		Contacts      int     `json:"contacts"`
		ElapsedSecond float64 `json:"elapsed_seconds"`
	} `json:"result,omitempty"`
}

type pairRequest struct {
	FullHistory bool `json:"full_history"`
}

func pairingViewOf(session *botflota.PairingSession) pairingView {
	view := pairingView{
		Scope: session.Scope, Bot: session.Bot, Method: session.Method,
		State: string(session.State), Code: session.Code,
		StartedAt: session.StartedAt, UpdatedAt: session.UpdatedAt,
		ExpiresAt: session.ExpiresAt, Error: session.Error,
	}
	if session.State == botflota.PairSucceeded {
		view.Result = &struct {
			Phone         string  `json:"phone"`
			Chats         int     `json:"chats"`
			Messages      int     `json:"messages"`
			Duplicate     int     `json:"duplicate"`
			Skipped       int     `json:"skipped"`
			Contacts      int     `json:"contacts"`
			ElapsedSecond float64 `json:"elapsed_seconds"`
		}{
			Phone: session.Result.Phone, Chats: session.Result.Chats,
			Messages:  session.Result.Messages,
			Duplicate: session.Result.Duplicate,
			Skipped:   session.Result.Skipped, Contacts: session.Result.Contacts,
			ElapsedSecond: session.Result.Elapsed.Seconds(),
		}
	}
	return view
}

// handlePairStart refuses a bot that is already paired: replacing a pairing
// is a local, stopped-fleet operation until Phase 12 controls arrive.
func (s *Server) handlePairStart(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	var request pairRequest
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request)
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if _, err := s.accounts.Get(r.Context(), scope, bot); err == nil {
		writeError(w, http.StatusConflict, "already_paired",
			"bot is already paired; replace it with bot pair while the fleet is stopped")
		return
	} else if !errors.Is(err, botflota.ErrNotFound) {
		s.fail(w, "read account", err)
		return
	}
	session, err := s.pairing.StartPair(
		r.Context(), scope, bot, botflota.PairRequest{FullHistory: request.FullHistory})
	switch {
	case errors.Is(err, botflota.ErrBusy):
		writeError(w, http.StatusConflict, "pairing_in_progress",
			"a pairing session is already running")
	case err != nil:
		s.fail(w, "start pairing", err)
	default:
		writeJSON(w, http.StatusAccepted, pairingViewOf(session))
	}
}

func (s *Server) handlePairState(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	session, err := s.pairing.CurrentPair(r.Context(), scope, bot)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no pairing session for that bot")
		return
	}
	if err != nil {
		s.fail(w, "read pairing", err)
		return
	}
	writeJSON(w, http.StatusOK, pairingViewOf(session))
}

func (s *Server) handlePairCancel(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	err := s.pairing.CancelPair(r.Context(), scope, bot)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no pairing session for that bot")
		return
	}
	if err != nil {
		s.fail(w, "cancel pairing", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
