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
	JID      string `json:"jid,omitempty"`
	Pairing  string `json:"pairing,omitempty"`
	State    string `json:"state,omitempty"`
	Outbox   int    `json:"outbox"`
	Sessions int    `json:"sessions"`
}

type senderView struct {
	Phone string `json:"phone"`
	Name  string `json:"name,omitempty"`
}

type chatView struct {
	ID        int64      `json:"id"`
	Scope     string     `json:"scope"`
	Bot       string     `json:"bot"`
	Sender    senderView `json:"sender"`
	StartedAt time.Time  `json:"started_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	// Messages is the stored history total; Sync and Live split it by
	// ingest kind.
	Messages int64 `json:"messages"`
	Sync     int64 `json:"sync"`
	Live     int64 `json:"live"`
}

type contactView struct {
	Scope string `json:"scope"`
	Bot   string `json:"bot"`
	Phone string `json:"phone"`
	// DisplayName resolves Name, BusinessName, and PushName, which stay raw.
	DisplayName  string    `json:"display_name,omitempty"`
	Name         string    `json:"name,omitempty"`
	PushName     string    `json:"push_name,omitempty"`
	BusinessName string    `json:"business_name,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
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

func (s *Server) activeSessions(ctx context.Context, scope, bot string) (int, error) {
	return s.sessions.CountActive(ctx, scope, bot, time.Now())
}
