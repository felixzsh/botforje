package api

import (
	"context"
	"net/http"

	"github.com/felixzsh/botflota"
)

// chatController is the live fleet's per-chat control capability (ADR 0043).
// *fleet.Fleet satisfies it; a server without one answers 501 on the route and
// reports no control in the chat list.
type chatController interface {
	SetChatHuman(ctx context.Context, scope, bot, phone string, human bool) error
	ChatControl(scope, bot, phone string) (botflota.ChatControl, bool)
}

// handleChatControl holds one chat for a human or releases it (ADR 0043). The
// automatic pause needs no route: it starts from an external reply.
func (s *Server) handleChatControl(w http.ResponseWriter, r *http.Request) {
	scope, bot, chat, ok := s.chatParams(w, r)
	if !ok {
		return
	}
	phone := r.URL.Query().Get("sender")
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	var human bool
	switch body.Mode {
	case string(botflota.ChatHuman):
		human = true
	case string(botflota.ChatAuto):
		human = false
	default:
		writeError(w, http.StatusUnprocessableEntity, "invalid_config",
			"mode must be human or auto")
		return
	}
	controller, ok := s.fleet.(chatController)
	if !ok {
		writeError(w, http.StatusNotImplemented, "unsupported",
			"chat control is not available")
		return
	}
	if err := controller.SetChatHuman(r.Context(), scope, bot, phone, human); err != nil {
		s.fail(w, "set chat control", err)
		return
	}
	state, _ := controller.ChatControl(scope, bot, phone)
	writeJSON(w, http.StatusOK, map[string]any{
		"scope": scope, "bot": bot, "sender": phone, "chat_id": chat.ID,
		"control": controlView(state),
	})
}

// controlView is the wire shape of one chat's control.
func controlView(state botflota.ChatControl) map[string]any {
	view := map[string]any{"mode": string(state.Mode)}
	if !state.PausedTill.IsZero() {
		view["paused_till"] = state.PausedTill
	}
	return view
}

// chatControl reads one chat's live control when the fleet can report it.
func (s *Server) chatControl(scope, bot, phone string) (botflota.ChatControl, bool) {
	controller, ok := s.fleet.(chatController)
	if !ok {
		return botflota.ChatControl{}, false
	}
	return controller.ChatControl(scope, bot, phone)
}
