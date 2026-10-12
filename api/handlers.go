package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
)

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

func (s *Server) handleBots(w http.ResponseWriter, r *http.Request) {
	principal := principalFrom(r.Context())
	bots := make([]botView, 0)
	for _, scope := range config.SortedKeys(s.config().Scopes) {
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

// handleScopeBot reads one bot of a scope, or answers 404 for an unknown bot
// like every other scope-owned target (ADR 0044).
func (s *Server) handleScopeBot(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	view, err := s.scopeBotView(r.Context(), scope, bot)
	if err != nil {
		s.fail(w, "read bot", err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleChats lists the chats of a scope, newest first. An empty bot lists
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
	limit, err := limitParam(r, true)
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
		view := chatView{
			ID:    chat.ID,
			Scope: chat.Scope,
			Bot:   chat.Bot,
			Contact: peerView{
				Provider: chat.PeerKey.Provider,
				Subject:  chat.PeerKey.Subject,
				Phone:    chat.PeerKey.Subject,
				Name:     chat.DisplayName,
			},
			StartedAt: chat.StartedAt,
			UpdatedAt: chat.UpdatedAt,
			Messages:  chat.Messages,
			Sync:      chat.Sync,
			Live:      chat.Live,
		}
		if state, ok := s.chatControl(scope, chat.Bot, chat.Target); ok {
			view.Control = string(state.Mode)
			if !state.PausedTill.IsZero() {
				paused := state.PausedTill
				view.PausedTill = &paused
			}
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": views})
}

// historySyncer is the live fleet's optional history capability (ADR 0035);
// fleet.Fleet implements it.
type historySyncer interface {
	HistorySync(
		ctx context.Context, scope, bot string, target botflota.ChatTarget,
		count int, timeout time.Duration,
	) (botflota.HistoryResult, error)
}

func (s *Server) handleChatSync(w http.ResponseWriter, r *http.Request) {
	scope, bot, chat, ok := s.chatParams(w, r)
	if !ok {
		return
	}
	syncer, ok := s.fleet.(historySyncer)
	if !ok {
		writeError(w, http.StatusNotImplemented, "unsupported_operation",
			"on-demand history is not available")
		return
	}
	count, err := countParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	timeout, err := timeoutParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	phone := r.URL.Query().Get("contact")
	result, err := syncer.HistorySync(r.Context(), scope, bot,
		legacyWhatsAppTarget(phone), count, timeout)
	if err != nil {
		// The upstream failure names the peer, the device, or the provider;
		// the caller gets the shape, the log gets the detail.
		s.log.Error("api request failed", "operation", "history sync",
			"scope", scope, "bot", bot, "error", err)
		writeError(w, http.StatusBadGateway, "sync_failed",
			"the history request to the channel failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":     scope,
		"bot":       bot,
		"contact":   phone,
		"chat_id":   chat.ID,
		"received":  result.Received,
		"rounds":    result.Rounds,
		"imported":  result.Imported,
		"duplicate": result.Duplicate,
		"skipped":   result.Skipped,
		"stable":    result.Stable,
		"exhausted": result.Exhausted,
	})
}

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

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	scope, bot, chat, ok := s.chatParams(w, r)
	if !ok {
		return
	}
	limit, err := limitParam(r, false)
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

// handleConfig serves the typed model to the instance owner; every secret
// marshals masked.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !principalFrom(r.Context()).Owner() {
		writeError(w, http.StatusForbidden, "forbidden", "instance owner required")
		return
	}
	writeJSON(w, http.StatusOK, s.config())
}

// handleScopeConfig serves one scope as the files hold it, never as the
// running instance holds it: an editor must write against the saved document,
// or it would compute a base for a change that is already on disk but not yet
// active. A missing display_name stays absent, so presentation may fall back
// to the key without changing the document.
func (s *Server) handleScopeConfig(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.configScopeParam(w, r)
	if !ok {
		return
	}
	saved, present := s.savedConfig().Scopes[scope]
	status := "active"
	if !present {
		// The files no longer hold the scope while the instance still runs it.
		// Serve what is running and say so: there is no saved document to
		// edit, and calling it active would claim the opposite.
		saved = s.config().Scopes[scope]
		status = "unsaved"
	}
	version, err := config.ScopeVersion(saved)
	if err != nil {
		s.fail(w, "version saved scope", err)
		return
	}
	activeVersion, err := config.ScopeVersion(s.config().Scopes[scope])
	if err != nil {
		s.fail(w, "version active scope", err)
		return
	}
	// The version travels as an entity tag too, so a client can keep it for a
	// conditional write without parsing the body.
	w.Header().Set("ETag", `"`+version+`"`)
	if present && version != activeVersion {
		status = "pending"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":          scope,
		"version":        version,
		"active_version": activeVersion,
		"status":         status,
		"config":         saved,
	})
}
