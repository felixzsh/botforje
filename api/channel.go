package api

import (
	"net/http"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// channelCapabilitiesView is the declared support of one bot's installed
// definition in the public snake_case vocabulary, without credentials or
// backend locators (ADR 0052, ADR 0054).
type channelCapabilitiesView struct {
	Send         []botflota.MessageKind  `json:"send"`
	Receive      []botflota.MessageKind  `json:"receive"`
	Features     []string                `json:"features"`
	CardVariants []spec.ContactEntryKind `json:"card_variants"`
}

type channelView struct {
	Provider     string                  `json:"provider"`
	Adapter      string                  `json:"adapter"`
	Mode         string                  `json:"mode"`
	Capabilities channelCapabilitiesView `json:"capabilities"`
}

// handleChannelDiscovery exposes the installed definition one bot runs on:
// provider, adapter, mode, and declared capabilities. Declarations are
// static: a disconnected or stopped bot keeps them without invented current
// permission, and unknown observations never imply permission (ADR 0052).
// Binding readiness and conversation eligibility stay separate facts on
// their own routes (ADR 0053).
func (s *Server) handleChannelDiscovery(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	if s.channels == nil {
		writeError(w, http.StatusNotImplemented, "unsupported_operation",
			"channel discovery is not available")
		return
	}
	botSpec, ok := s.config().Scopes[scope].Bots[bot]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown bot")
		return
	}
	def, _, err := s.channels.Resolve(botSpec.Channel)
	if err != nil {
		s.fail(w, "resolve channel", err)
		return
	}
	caps := def.Capabilities
	view := channelView{
		Provider: def.Provider,
		Adapter:  def.Kind,
		Mode:     botSpec.Channel.Mode,
		Capabilities: channelCapabilitiesView{
			Send:         append([]botflota.MessageKind{}, caps.Send...),
			Receive:      append([]botflota.MessageKind{}, caps.Receive...),
			Features:     append([]string{}, caps.Features...),
			CardVariants: append([]spec.ContactEntryKind{}, caps.CardVariants...),
		},
	}
	if view.Capabilities.Send == nil {
		view.Capabilities.Send = []botflota.MessageKind{}
	}
	if view.Capabilities.Receive == nil {
		view.Capabilities.Receive = []botflota.MessageKind{}
	}
	if view.Capabilities.Features == nil {
		view.Capabilities.Features = []string{}
	}
	if view.Capabilities.CardVariants == nil {
		view.Capabilities.CardVariants = []spec.ContactEntryKind{}
	}
	writeJSON(w, http.StatusOK, view)
}
