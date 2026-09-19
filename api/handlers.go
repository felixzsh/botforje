package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
)

// Limits for the ?limit= query parameter.
const (
	defaultLimit = 50
	maxLimit     = 500
)

// botView is one configured bot as the API reports it: its identity, its
// stored pairing, and its live state.
type botView struct {
	Scope   string `json:"scope"`
	Bot     string `json:"bot"`
	JID     string `json:"jid,omitempty"`
	Pairing string `json:"pairing,omitempty"`
	State   string `json:"state,omitempty"`
	Outbox  int    `json:"outbox"`
}

// senderView is the profile of the person on the other side of a chat.
type senderView struct {
	Phone string `json:"phone"`
	Name  string `json:"name,omitempty"`
}

// chatView is one chat with its sender profile.
type chatView struct {
	ID        int64      `json:"id"`
	Scope     string     `json:"scope"`
	Bot       string     `json:"bot"`
	Sender    senderView `json:"sender"`
	StartedAt time.Time  `json:"started_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Messages  int64      `json:"messages"`
}

// messageView is one stored message.
type messageView struct {
	ID        int64                `json:"id"`
	ChatID    int64                `json:"chat_id"`
	SessionID int64                `json:"session_id,omitempty"`
	Direction botflota.Direction   `json:"direction"`
	Kind      botflota.MessageKind `json:"kind"`
	Body      string               `json:"body"`
	At        time.Time            `json:"at"`
}

// sessionView is the latest graph run of a chat.
type sessionView struct {
	ID           int64     `json:"id"`
	ChatID       int64     `json:"chat_id"`
	Node         string    `json:"node"`
	VisitedNodes []string  `json:"visited_nodes"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	statuses := s.fleet.Status()
	bots := make([]botView, 0, len(statuses))
	for _, status := range statuses {
		bots = append(bots, botView{
			Scope:  status.Scope,
			Bot:    status.Bot,
			State:  status.State,
			Outbox: status.OutboxDepth,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_seconds": int(time.Since(s.started).Seconds()),
		"bots":           bots,
	})
}

func (s *Server) handleBots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	live := make(map[string]botView)
	for _, status := range s.fleet.Status() {
		live[status.Scope+"\x00"+status.Bot] = botView{
			State:  status.State,
			Outbox: status.OutboxDepth,
		}
	}
	bots := make([]botView, 0)
	for _, scope := range config.SortedKeys(s.cfg.Scopes) {
		for _, bot := range config.SortedKeys(s.cfg.Scopes[scope].Bots) {
			view := live[scope+"\x00"+bot]
			view.Scope = scope
			view.Bot = bot
			account, err := s.accounts.Get(ctx, scope, bot)
			switch {
			case err == nil:
				view.JID = account.JID
			case errors.Is(err, botflota.ErrNotFound):
			default:
				s.fail(w, "read linked account", err)
				return
			}
			pairing, err := s.pairings.Get(ctx, scope, bot)
			switch {
			case err == nil:
				view.Pairing = string(pairing.State)
			case errors.Is(err, botflota.ErrNotFound):
			default:
				s.fail(w, "read pairing status", err)
				return
			}
			bots = append(bots, view)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bots": bots})
}

// handleChats lists the chats of a scope, newest first. An empty bot means
// every bot in the scope.
func (s *Server) handleChats(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botParam(w, r, scope)
	if !ok {
		return
	}
	limit, err := limitParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	chats, err := s.chats.Recent(r.Context(), scope, bot, limit)
	if err != nil {
		s.fail(w, "list chats", err)
		return
	}
	views := make([]chatView, 0, len(chats))
	for _, chat := range chats {
		views = append(views, chatView{
			ID:    chat.ID,
			Scope: chat.Scope,
			Bot:   chat.Bot,
			Sender: senderView{
				Phone: chat.Phone,
				Name:  chat.DisplayName,
			},
			StartedAt: chat.StartedAt,
			UpdatedAt: chat.UpdatedAt,
			Messages:  chat.MessageCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": views})
}

// handleSessions reports the latest graph run of one chat. The chat is
// addressed by scope, bot, and the sender's phone.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	scope, bot, chat, ok := s.chatParams(w, r)
	if !ok {
		return
	}
	session, err := s.sessions.Latest(r.Context(), chat.ID)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no session for that chat")
		return
	}
	if err != nil {
		s.fail(w, "read session", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":   scope,
		"bot":     bot,
		"chat_id": chat.ID,
		"session": sessionView{
			ID:           session.ID,
			ChatID:       session.ChatID,
			Node:         session.Node,
			VisitedNodes: session.VisitedNodes,
			CreatedAt:    session.CreatedAt,
			UpdatedAt:    session.UpdatedAt,
			ExpiresAt:    session.ExpiresAt,
		},
	})
}

// handleMessages lists the stored messages of one chat, oldest first. The chat
// is addressed by scope, bot, and the sender's phone, like the CLI.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	scope, bot, chat, ok := s.chatParams(w, r)
	if !ok {
		return
	}
	limit, err := limitParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	messages, err := s.messages.List(r.Context(), chat.ID, limit)
	if err != nil {
		s.fail(w, "list messages", err)
		return
	}
	views := make([]messageView, 0, len(messages))
	for _, message := range messages {
		views = append(views, messageView{
			ID:        message.ID,
			ChatID:    message.ChatID,
			SessionID: message.SessionID,
			Direction: message.Direction,
			Kind:      message.Kind,
			Body:      message.Body,
			At:        message.At,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":    scope,
		"bot":      bot,
		"chat_id":  chat.ID,
		"messages": views,
	})
}

// handleConfig serves the typed model the loader produced, so the Phase 8
// catalogs surface without reworking handlers. Secrets marshal masked
// (ADR 0014).
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}

// chatParams reads the required scope, bot, and sender of a chat address.
func (s *Server) chatParams(
	w http.ResponseWriter, r *http.Request,
) (scope, bot string, chat *botflota.Chat, ok bool) {
	scope, ok = s.scopeParam(w, r)
	if !ok {
		return "", "", nil, false
	}
	bot, ok = s.botParam(w, r, scope)
	if !ok {
		return "", "", nil, false
	}
	if bot == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "bot is required")
		return "", "", nil, false
	}
	phone := r.URL.Query().Get("sender")
	if phone == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "sender is required")
		return "", "", nil, false
	}
	ctx := r.Context()
	sender, err := s.senders.Get(ctx, scope, phone)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "unknown sender")
		return "", "", nil, false
	}
	if err != nil {
		s.fail(w, "read sender", err)
		return "", "", nil, false
	}
	chat, err = s.chats.ForSender(ctx, scope, bot, sender.ID)
	if errors.Is(err, botflota.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no chat for that sender")
		return "", "", nil, false
	}
	if err != nil {
		s.fail(w, "read chat", err)
		return "", "", nil, false
	}
	return scope, bot, chat, true
}

// scopeParam reads and checks ?scope=.
func (s *Server) scopeParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "scope is required")
		return "", false
	}
	if _, ok := s.cfg.Scopes[scope]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown scope")
		return "", false
	}
	return scope, true
}

// botParam reads ?bot= and checks it against the scope. An empty bot is
// allowed; the caller decides whether it needs one.
func (s *Server) botParam(w http.ResponseWriter, r *http.Request, scope string) (string, bool) {
	bot := r.URL.Query().Get("bot")
	if bot == "" {
		return "", true
	}
	if _, ok := s.cfg.Scopes[scope].Bots[bot]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown bot")
		return "", false
	}
	return bot, true
}

// limitParam reads ?limit=, defaulting to defaultLimit and capping at
// maxLimit.
func limitParam(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	if limit > maxLimit {
		return maxLimit, nil
	}
	return limit, nil
}

// fail reports an internal read error and hides its detail from the client.
func (s *Server) fail(w http.ResponseWriter, operation string, err error) {
	s.log.Error("api request failed", "operation", operation, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}
