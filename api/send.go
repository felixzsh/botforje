package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/felixzsh/botflota"
)

// sendTarget is the neutral conversation selector of an outbound send: the
// provider, kind, and thread it names, with P1's canonical empty topic when
// the conversation is unthreaded (ADR 0044, ADR 0051).
type sendTarget struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Thread   string `json:"thread"`
	Topic    string `json:"topic"`
}

// sendRecipient is the optional known messaging identity of a send,
// separate from its destination (ADR 0044).
type sendRecipient struct {
	Provider string `json:"provider"`
	Subject  string `json:"subject"`
}

// handleSendMessage stores and queues one message from the API to a
// conversation of one (scope, bot): no session turn, origin api, delivered
// by the bot's outbox with the pacing of every reply (ADR 0044). The
// destination is exactly one of chat_id or target; the retired string to
// and phone selectors are invalid (ADR 0054).
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Bot       string         `json:"bot"`
		ChatID    *int64         `json:"chat_id"`
		Target    *sendTarget    `json:"target"`
		Recipient *sendRecipient `json:"recipient"`
		Body      string         `json:"body"`
		Kind      string         `json:"kind"`
		ClientID  string         `json:"client_id"`
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
	if (body.ChatID == nil) == (body.Target == nil) {
		writeError(w, http.StatusBadRequest, "invalid_request",
			"exactly one of chat_id or target is required")
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
		writeError(w, http.StatusNotImplemented, "unsupported_operation",
			"sending is not available")
		return
	}
	target, recipient, ok := s.sendDestination(w, r, scope, bot, body.ChatID,
		body.Target, body.Recipient)
	if !ok {
		return
	}
	if body.ClientID != "" {
		// Reconciliation creates no new send: a lost-response retry
		// reports the accepted message even after the binding changed,
		// so only a new admission requires one (ADR 0044).
		accepted, err := s.sends.FindSend(r.Context(), scope, bot, body.ClientID)
		if err == nil {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]string{
					"code":    "duplicate",
					"message": "client_id was already accepted",
				},
				"id": accepted.MessageID,
			})
			return
		}
		if !errors.Is(err, botflota.ErrNotFound) {
			s.fail(w, "reconcile send", err)
			return
		}
	}
	if _, err := s.bindings.Get(r.Context(), scope, bot); err != nil {
		if errors.Is(err, botflota.ErrNotFound) {
			// No peer, chat, message, or outbox row is created for an
			// unbound send; a disconnected bound bot is not unbound
			// (ADR 0044).
			writeError(w, http.StatusConflict, "binding_required",
				"that bot has no committed binding")
			return
		}
		s.fail(w, "read binding", err)
		return
	}
	if !s.admitSend(w, scope, bot, target) {
		return
	}
	result, err := s.sends.CommitSend(r.Context(), botflota.SendRecord{
		Scope: scope, Bot: bot, Target: target, Recipient: recipient,
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

// sendDestination resolves the send's destination and known recipient. A
// chat id selects the stored conversation target under this scope and bot,
// never the sender or a derived address; an explicit target carries its own
// provider, kind, and thread (ADR 0044).
func (s *Server) sendDestination(
	w http.ResponseWriter, r *http.Request, scope, bot string,
	chatID *int64, target *sendTarget, recipient *sendRecipient,
) (botflota.ChatTarget, *botflota.IdentityKey, bool) {
	var known int64
	var destination botflota.ChatTarget
	if chatID != nil {
		chat, err := s.chats.ByID(r.Context(), *chatID)
		if err != nil || chat.Scope != scope || chat.Bot != bot {
			// A foreign chat is indistinguishable from an unknown one, so
			// one scope's ids never probe another's conversations.
			if err != nil && !errors.Is(err, botflota.ErrNotFound) {
				s.fail(w, "read chat", err)
				return destination, nil, false
			}
			writeError(w, http.StatusNotFound, "not_found",
				"no chat with that id")
			return destination, nil, false
		}
		destination, known = chat.Target, chat.PeerID
	} else {
		destination = botflota.ChatTarget{
			Provider: target.Provider,
			Kind:     botflota.ChatKind(target.Kind),
			Thread:   target.Thread,
			Topic:    target.Topic,
		}
		if !destination.Valid() {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"target needs provider, kind, and thread")
			return destination, nil, false
		}
	}
	var knownRecipient *botflota.IdentityKey
	if recipient != nil {
		key := botflota.IdentityKey{
			Provider: recipient.Provider, Subject: recipient.Subject,
		}
		if !key.Valid() {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"recipient needs provider and subject")
			return destination, nil, false
		}
		if known != 0 {
			// A supplied recipient must not contradict the chat's known
			// participant; an unknown identity is left for the store to
			// register (ADR 0044).
			peer, err := s.peers.Get(r.Context(), scope, key)
			if err == nil && peer.ID != known {
				writeError(w, http.StatusConflict, "conflict",
					"recipient contradicts the chat's known participant")
				return destination, nil, false
			}
			if err != nil && !errors.Is(err, botflota.ErrNotFound) {
				s.fail(w, "read recipient", err)
				return destination, nil, false
			}
		}
		knownRecipient = &key
	}
	return destination, knownRecipient, true
}

// admitSend applies the selected adapter's target rules and declared content
// support before queue admission. A mismatched provider, an unsupported
// target, or undeclared content is refused before persistence; deeper
// delivery-time checks stay with the outbox (ADR 0044, ADR 0052).
func (s *Server) admitSend(
	w http.ResponseWriter, scope, bot string, target botflota.ChatTarget,
) bool {
	if s.channels == nil {
		return true
	}
	botSpec, ok := s.config().Scopes[scope].Bots[bot]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown bot")
		return false
	}
	def, _, err := s.channels.Resolve(botSpec.Channel)
	if err != nil {
		s.fail(w, "resolve channel", err)
		return false
	}
	if target.Provider != botSpec.Channel.Provider {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_operation",
			"target provider does not match the bot's channel")
		return false
	}
	if !def.Capabilities.SupportsSend(botflota.KindText) {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_operation",
			"that channel cannot send text")
		return false
	}
	if def.ValidateTarget != nil {
		if reasons := def.ValidateTarget(target); len(reasons) > 0 {
			writeError(w, http.StatusUnprocessableEntity, "unsupported_operation",
				reasons[0])
			return false
		}
	}
	return true
}
