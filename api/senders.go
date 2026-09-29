package api

import (
	"errors"
	"net/http"

	"github.com/felixzsh/botflota"
)

// handleSenders lists one scope's sender profiles, most recently seen first,
// with an opaque cursor for the next page (ADR 0044).
func (s *Server) handleSenders(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	limit, err := limitParam(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	since, err := sinceParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	page, err := s.senders.List(r.Context(), scope, since, limit,
		r.URL.Query().Get("cursor"))
	if errors.Is(err, botflota.ErrInvalidCursor) {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"cursor is not valid for this read")
		return
	}
	if err != nil {
		s.fail(w, "list senders", err)
		return
	}
	views := make([]senderProfileView, 0, len(page.Senders))
	for _, sender := range page.Senders {
		views = append(views, senderProfile(sender))
	}
	body := map[string]any{"scope": scope, "senders": views}
	if page.NextCursor != "" {
		body["next_cursor"] = page.NextCursor
	}
	writeJSON(w, http.StatusOK, body)
}

// handleSender reads one profile, or answers 404 like any other unknown
// scope-owned target (ADR 0044).
func (s *Server) handleSender(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	phone := r.PathValue("phone")
	sender, err := s.senders.Get(r.Context(), scope, phone)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "unknown sender")
		return
	}
	if err != nil {
		s.fail(w, "read sender", err)
		return
	}
	writeJSON(w, http.StatusOK, senderProfile(*sender))
}

func senderProfile(sender botflota.Sender) senderProfileView {
	return senderProfileView{
		Phone:       sender.Phone,
		DisplayName: sender.DisplayName,
		FirstSeen:   sender.FirstSeen,
		LastSeen:    sender.LastSeen,
	}
}
