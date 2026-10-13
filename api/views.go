package api

import (
	"context"
	"errors"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
)

type botView struct {
	Scope    string `json:"scope"`
	Bot      string `json:"bot"`
	State    string `json:"state,omitempty"`
	Outbox   int    `json:"outbox"`
	Sessions int    `json:"sessions"`
	// Binding is the committed account association, null when the bot has
	// none. Readiness, capabilities, and setup stay on their own routes
	// (ADR 0052, ADR 0053).
	Binding *bindingView `json:"binding"`
}

// peerRefView is the peer a chat is with: its neutral reference and
// best-effort name, never a phone projection (ADR 0051, ADR 0054).
type peerRefView struct {
	Provider string `json:"provider,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Name     string `json:"name,omitempty"`
}

// peerView is the scope-level identity read (ADR 0044). ID is the
// scope-owned internal reference a caller passes back; DisplayName is the
// best-effort push name, always present, possibly empty. FirstSeen and
// LastSeen are zero without a live inbound. The other fields derive whether
// the peer came from a directory import and whether chat or history exists.
type peerView struct {
	ID                int64     `json:"id"`
	Provider          string    `json:"provider,omitempty"`
	Subject           string    `json:"subject,omitempty"`
	DisplayName       string    `json:"display_name"`
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
	DirectoryImported bool      `json:"directory_imported"`
	HasChat           bool      `json:"has_chat"`
	HasHistory        bool      `json:"has_history"`
}

type chatView struct {
	ID        int64       `json:"id"`
	Scope     string      `json:"scope"`
	Bot       string      `json:"bot"`
	Peer      peerRefView `json:"peer"`
	StartedAt time.Time   `json:"started_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	// Messages is the stored history total; Sync and Live split it by
	// ingest kind.
	Messages int64 `json:"messages"`
	Sync     int64 `json:"sync"`
	Live     int64 `json:"live"`
	// Control is the live automation of the chat: auto, paused, or human
	// (ADR 0043). PausedTill is set only while an automatic pause counts down.
	Control    string     `json:"control,omitempty"`
	PausedTill *time.Time `json:"paused_till,omitempty"`
}

// directoryEntryView is one imported directory entry (ADR 0037, ADR 0051):
// the peer it describes and the account whose book supplied it, never a
// phone projection.
type directoryEntryView struct {
	Scope string `json:"scope"`
	Bot   string `json:"bot"`
	// Provider and Subject are the peer the entry describes; SourceProvider and
	// SourceSubject are the account whose book supplied it.
	Provider       string    `json:"provider,omitempty"`
	Subject        string    `json:"subject,omitempty"`
	SourceProvider string    `json:"source_provider,omitempty"`
	SourceSubject  string    `json:"source_subject,omitempty"`
	DisplayName    string    `json:"display_name,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Kind and Body are the derived summary of Content.
type messageView struct {
	ID         int64                   `json:"id"`
	ChatID     int64                   `json:"chat_id"`
	SessionID  int64                   `json:"session_id,omitempty"`
	Direction  botflota.Direction      `json:"direction"`
	Kind       botflota.MessageKind    `json:"kind"`
	IngestKind botflota.Ingest         `json:"ingest_kind"`
	Origin     botflota.MessageOrigin  `json:"origin,omitempty"`
	Routing    botflota.RoutingMethod  `json:"routing"`
	Body       string                  `json:"body"`
	Content    botflota.MessageContent `json:"content"`
	At         time.Time               `json:"at"`
}

type sessionView struct {
	ID           int64     `json:"id"`
	ChatID       int64     `json:"chat_id"`
	Node         string    `json:"node"`
	VisitedNodes []string  `json:"visited_nodes"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (s *Server) botsView(ctx context.Context, scope string) ([]botView, error) {
	live := make(map[string]botView)
	for _, status := range s.fleet.Status() {
		if status.Scope == scope {
			live[status.Bot] = botView{State: status.State, Outbox: status.OutboxDepth}
		}
	}
	bots := make([]botView, 0)
	for _, bot := range config.SortedKeys(s.config().Scopes[scope].Bots) {
		view, err := s.botView(ctx, scope, bot, live[bot])
		if err != nil {
			return nil, err
		}
		bots = append(bots, view)
	}
	return bots, nil
}

// botView reads one configured bot's view; live carries the fleet status when
// the bot is running. The scope list and the single-bot read share it.
func (s *Server) botView(
	ctx context.Context, scope, bot string, live botView,
) (botView, error) {
	view := live
	view.Scope = scope
	view.Bot = bot
	sessions, err := s.activeSessions(ctx, scope, bot)
	if err != nil {
		return botView{}, err
	}
	view.Sessions = sessions
	account, err := s.bindings.Get(ctx, scope, bot)
	switch {
	case err == nil:
		view.Binding = bindingViewOf(account)
	case errors.Is(err, botflota.ErrNotFound):
	default:
		return botView{}, err
	}
	return view, nil
}

// scopeBotView is the single-bot read: the same view, with the fleet's status
// looked up for that bot only.
func (s *Server) scopeBotView(ctx context.Context, scope, bot string) (botView, error) {
	var live botView
	for _, status := range s.fleet.Status() {
		if status.Scope == scope && status.Bot == bot {
			live = botView{State: status.State, Outbox: status.OutboxDepth}
		}
	}
	return s.botView(ctx, scope, bot, live)
}

func (s *Server) activeSessions(ctx context.Context, scope, bot string) (int, error) {
	return s.sessions.CountActive(ctx, scope, bot, time.Now())
}
