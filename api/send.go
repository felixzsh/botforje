package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/felixzsh/botflota"
)

// handleSendMessage stores and queues one message from the API to a phone of
// one (scope, bot): no session turn, origin api, delivered by the bot's
// outbox with the pacing of every reply (ADR 0044).
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Bot      string `json:"bot"`
		To       string `json:"to"`
		Body     string `json:"body"`
		Kind     string `json:"kind"`
		ClientID string `json:"client_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Bot == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "bot is required")
		return
	}
	bot, ok := s.knownBot(w, scope, body.Bot)
	if !ok {
		return
	}
	if !plainDigits(body.To) {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"to must be a phone number in plain digits")
		return
	}
	if body.Body == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "body is required")
		return
	}
	if body.Kind != "" && body.Kind != string(botflota.KindText) {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"kind must be text")
		return
	}
	if s.sends == nil {
		writeError(w, http.StatusNotImplemented, "unsupported",
			"sending is not available")
		return
	}
	result, err := s.sends.CommitSend(r.Context(), botflota.SendRecord{
		Scope: scope, Bot: bot, To: body.To,
		Content:  botflota.TextContent{Body: body.Body},
		ClientID: body.ClientID, At: time.Now(),
	})
	if errors.Is(err, botflota.ErrDuplicate) {
		// The accepted message id sits beside the error, so a caller that
		// lost the first response can reconcile (ADR 0044).
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{
				"code":    "duplicate",
				"message": "client_id was already accepted",
			},
			"id": result.MessageID,
		})
		return
	}
	if err != nil {
		s.fail(w, "send message", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id": result.MessageID, "status": "queued",
	})
}

// plainDigits reports a phone number in the plain-digit form a scope keys its
// senders on (ADR 0020).
func plainDigits(phone string) bool {
	if phone == "" {
		return false
	}
	for _, digit := range phone {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
