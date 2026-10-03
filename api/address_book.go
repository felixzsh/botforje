package api

import (
	"net/http"
)

// handleAddressBook lists the address-book entries one bot's linked account
// imported, most recently updated first (ADR 0037).
func (s *Server) handleAddressBook(w http.ResponseWriter, r *http.Request) {
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
	entries, err := s.addressBook.List(r.Context(), scope, bot, limit)
	if err != nil {
		s.fail(w, "list address book", err)
		return
	}
	views := make([]addressBookView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, addressBookView{
			Scope:        entry.Scope,
			Bot:          entry.Bot,
			Phone:        entry.Phone,
			DisplayName:  entry.DisplayName(),
			Name:         entry.Name,
			PushName:     entry.PushName,
			BusinessName: entry.BusinessName,
			UpdatedAt:    entry.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope": scope, "bot": bot, "contacts": views,
	})
}
