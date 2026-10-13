package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/felixzsh/botflota"
)

// handlePeers lists one scope's peer identities, most recently active first,
// with an opaque cursor for the next page (ADR 0044). It is a scope-level
// read: it never enumerates another scope's identities.
func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
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
		s.fail(w, "list peers", err)
		return
	}
	views := make([]peerView, 0, len(page.Peers))
	for _, peer := range page.Peers {
		views = append(views, peerProfile(peer))
	}
	body := map[string]any{"scope": scope, "peers": views}
	if page.NextCursor != "" {
		body["next_cursor"] = page.NextCursor
	}
	writeJSON(w, http.StatusOK, body)
}

// handlePeer reads one identity by its scope-owned internal id, or answers
// 404 like any other scope-owned target (ADR 0044). A subject alone is never
// a cross-provider lookup key, so the id carries the scope's ownership.
func (s *Server) handlePeer(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "unknown peer")
		return
	}
	peer, err := s.peers.GetByID(r.Context(), scope, id)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "unknown peer")
		return
	}
	if err != nil {
		s.fail(w, "read peer", err)
		return
	}
	writeJSON(w, http.StatusOK, peerProfile(*peer))
}

func peerProfile(peer botflota.Peer) peerView {
	return peerView{
		ID:                peer.ID,
		Provider:          peer.Key.Provider,
		Subject:           peer.Key.Subject,
		DisplayName:       peer.DisplayName,
		FirstSeen:         peer.FirstSeen,
		LastSeen:          peer.LastSeen,
		DirectoryImported: peer.AddressBookImported,
		HasChat:           peer.HasChat,
		HasHistory:        peer.HasHistory,
	}
}
