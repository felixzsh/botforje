package api

import (
	"errors"
	"net/http"

	"github.com/felixzsh/botflota"
)

// handleContacts lists one scope's contact identities, most recently seen
// first, with an opaque cursor for the next page (ADR 0044).
func (s *Server) handleContacts(w http.ResponseWriter, r *http.Request) {
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
	page, err := s.peers.List(r.Context(), scope, since, limit,
		r.URL.Query().Get("cursor"))
	if errors.Is(err, botflota.ErrInvalidCursor) {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"cursor is not valid for this read")
		return
	}
	if err != nil {
		s.fail(w, "list contacts", err)
		return
	}
	views := make([]contactProfileView, 0, len(page.Peers))
	for _, peer := range page.Peers {
		views = append(views, contactProfile(peer))
	}
	body := map[string]any{"scope": scope, "contacts": views}
	if page.NextCursor != "" {
		body["next_cursor"] = page.NextCursor
	}
	writeJSON(w, http.StatusOK, body)
}

// handleContact reads one identity, or answers 404 like any other
// scope-owned target (ADR 0044).
func (s *Server) handleContact(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	subject := r.PathValue("subject")
	peer, err := s.peers.Get(r.Context(), scope, legacyWhatsAppPeer(subject))
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "unknown contact")
		return
	}
	if err != nil {
		s.fail(w, "read contact", err)
		return
	}
	writeJSON(w, http.StatusOK, contactProfile(*peer))
}

func contactProfile(peer botflota.Peer) contactProfileView {
	return contactProfileView{
		Provider:            peer.Key.Provider,
		Subject:             peer.Key.Subject,
		Phone:               peer.Key.Subject,
		DisplayName:         peer.DisplayName,
		FirstSeen:           peer.FirstSeen,
		LastSeen:            peer.LastSeen,
		AddressBookImported: peer.AddressBookImported,
		HasChat:             peer.HasChat,
		HasHistory:          peer.HasHistory,
	}
}
