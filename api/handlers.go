package api

import (
	"context"
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
// stored pairing, its live state, and its active sessions.
type botView struct {
	Scope    string `json:"scope"`
	Bot      string `json:"bot"`
	JID      string `json:"jid,omitempty"`
	Pairing  string `json:"pairing,omitempty"`
	State    string `json:"state,omitempty"`
	Outbox   int    `json:"outbox"`
	Sessions int    `json:"sessions"`
}

// senderView is the profile of the person on the other side of a chat.
type senderView struct {
	Phone string `json:"phone"`
	Name  string `json:"name,omitempty"`
}

// chatView is one chat with its sender profile. Messages is the total stored
// history, split by ingest kind into Sync and Live (ADR 0035).
type chatView struct {
	ID        int64      `json:"id"`
	Scope     string     `json:"scope"`
	Bot       string     `json:"bot"`
	Sender    senderView `json:"sender"`
	StartedAt time.Time  `json:"started_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Messages  int64      `json:"messages"`
	Sync      int64      `json:"sync"`
	Live      int64      `json:"live"`
}

// messageView is one stored message. Kind and Body are the derived summary of
// Content, which carries the structured payload (ADR 0036). IngestKind,
// Origin, and Routing carry the provenance the engine recorded (ADR 0026);
// Origin is empty for an inbound message.
type messageView struct {
	ID         int64                    `json:"id"`
	ChatID     int64                    `json:"chat_id"`
	SessionID  int64                    `json:"session_id,omitempty"`
	Direction  botflota.Direction       `json:"direction"`
	Kind       botflota.MessageKind     `json:"kind"`
	IngestKind botflota.Ingest          `json:"ingest_kind"`
	Origin     botflota.MessageOrigin   `json:"origin,omitempty"`
	Routing    botflota.RoutingMethod   `json:"routing"`
	Body       string                   `json:"body"`
	Content    botflota.MessageContent  `json:"content"`
	At         time.Time                `json:"at"`
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

// handleStatus reports the live fleet, filtered to the scopes the principal
// may see.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r.Context())
	statuses := s.fleet.Status()
	bots := make([]botView, 0, len(statuses))
	for _, status := range statuses {
		if !principal.Allows(status.Scope) {
			continue
		}
		sessions, err := s.activeSessions(r.Context(), status.Scope, status.Bot)
		if err != nil {
			s.fail(w, "count sessions", err)
			return
		}
		bots = append(bots, botView{
			Scope:    status.Scope,
			Bot:      status.Bot,
			State:    status.State,
			Outbox:   status.OutboxDepth,
			Sessions: sessions,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_seconds": int(time.Since(s.started).Seconds()),
		"bots":           bots,
	})
}

// handleBots reports every configured bot the principal may see.
func (s *Server) handleBots(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r.Context())
	bots := make([]botView, 0)
	for _, scope := range config.SortedKeys(s.cfg.Scopes) {
		if !principal.Allows(scope) {
			continue
		}
		views, err := s.botsView(r.Context(), scope)
		if err != nil {
			s.fail(w, "read bots", err)
			return
		}
		bots = append(bots, views...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"bots": bots})
}

// handleScopeBots reports the configured bots of one scope.
func (s *Server) handleScopeBots(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bots, err := s.botsView(r.Context(), scope)
	if err != nil {
		s.fail(w, "read bots", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scope": scope, "bots": bots})
}

// botsView joins the configured bots of one scope with their stored pairing
// and their live state.
func (s *Server) botsView(ctx context.Context, scope string) ([]botView, error) {
	live := make(map[string]botView)
	for _, status := range s.fleet.Status() {
		if status.Scope == scope {
			live[status.Bot] = botView{State: status.State, Outbox: status.OutboxDepth}
		}
	}
	bots := make([]botView, 0)
	for _, bot := range config.SortedKeys(s.cfg.Scopes[scope].Bots) {
		view := live[bot]
		view.Scope = scope
		view.Bot = bot
		sessions, err := s.activeSessions(ctx, scope, bot)
		if err != nil {
			return nil, err
		}
		view.Sessions = sessions
		account, err := s.accounts.Get(ctx, scope, bot)
		switch {
		case err == nil:
			view.JID = account.JID
		case errors.Is(err, botflota.ErrNotFound):
		default:
			return nil, err
		}
		pairing, err := s.pairings.Get(ctx, scope, bot)
		switch {
		case err == nil:
			view.Pairing = string(pairing.State)
		case errors.Is(err, botflota.ErrNotFound):
		default:
			return nil, err
		}
		bots = append(bots, view)
	}
	return bots, nil
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
			Messages:  chat.Messages,
			Sync:      chat.Sync,
			Live:      chat.Live,
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
// is addressed by scope, bot, and the sender's phone, like the CLI. Without a
// cursor it returns the last messages; with ?after_id= it returns the ones
// newer than that id, so a follower can poll.
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
	afterID, err := afterIDParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	messages, err := s.messages.History(r.Context(), chat.ID, afterID, limit)
	if err != nil {
		s.fail(w, "list messages", err)
		return
	}
	views := make([]messageView, 0, len(messages))
	for _, message := range messages {
		views = append(views, messageView{
			ID:         message.ID,
			ChatID:     message.ChatID,
			SessionID:  message.SessionID,
			Direction:  message.Direction,
			Kind:       botflota.ContentKind(message.Content),
			Body:       botflota.ContentText(message.Content),
			Content:    message.Content,
			IngestKind: message.Ingest,
			Origin:     message.Origin,
			Routing:    message.Routing,
			At:         message.At,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":    scope,
		"bot":      bot,
		"chat_id":  chat.ID,
		"messages": views,
	})
}

// handleConfig serves the full typed model to the instance owner; secrets
// marshal masked (ADR 0014).
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !principalFrom(r.Context()).Owner() {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner required")
		return
	}
	writeJSON(w, http.StatusOK, s.cfg)
}

// handleScopeConfig serves one scope of the typed model to any principal
// allowed on it.
func (s *Server) handleScopeConfig(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":  scope,
		"config": s.cfg.Scopes[scope],
	})
}

// chatParams reads the required bot and sender of a chat address.
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

// scopeParam reads {scope} from the path and returns it only when the
// principal may use it and the scope exists. An out-of-scope target answers
// 404, never 403, so a scoped principal cannot infer that another scope
// exists (ROADMAP Phase 13).
func (s *Server) scopeParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	scope := r.PathValue("scope")
	if !principalFrom(r.Context()).Allows(scope) {
		writeError(w, http.StatusNotFound, "not_found", "unknown scope")
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

// afterIDParam reads ?after_id=, defaulting to zero (no cursor). A follower
// polls with the last id it saw.
func afterIDParam(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("after_id")
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("after_id must be a non-negative integer")
	}
	return id, nil
}

// activeSessions counts the unexpired session runs of one bot for the status
// views.
func (s *Server) activeSessions(ctx context.Context, scope, bot string) (int, error) {
	return s.sessions.CountActive(ctx, scope, bot, time.Now())
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
