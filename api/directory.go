package api

import (
	"net/http"
)

// handleDirectory lists the directory entries one bot's account imported,
// most recently updated first (ADR 0037). Reading the retained snapshot is
// distinct from invoking a live directory import (ADR 0052).
func (s *Server) handleDirectory(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	limit, err := limitParam(r, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	entries, err := s.directory.List(r.Context(), scope, bot, limit)
	if err != nil {
		s.fail(w, "list directory", err)
		return
	}
	views := make([]directoryEntryView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, directoryEntryView{
			Scope:          entry.Scope,
			Bot:            entry.Bot,
			Provider:       entry.PeerKey.Provider,
			Subject:        entry.PeerKey.Subject,
			SourceProvider: entry.SourceIdentity.Provider,
			SourceSubject:  entry.SourceIdentity.Subject,
			DisplayName:    entry.DisplayName,
			UpdatedAt:      entry.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope": scope, "bot": bot, "directory": views,
	})
}
