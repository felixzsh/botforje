package api

import (
	"context"
	"errors"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
)

type botView struct {
	Scope   string `json:"scope"`
	Bot     string `json:"bot"`
	Phone   string `json:"phone,omitempty"`
	Pairing string `json:"pairing,omitempty"`
	// LastChange is when the pairing state last changed; it is shown only
	// while it matches the state reported above (ADR 0011).
	LastChange *time.Time `json:"last_change,omitempty"`
	State      string     `json:"state,omitempty"`
	Outbox     int        `json:"outbox"`
	Sessions   int        `json:"sessions"`
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
	for _, bot := range config.SortedKeys(s.config().Scopes[scope].Bots) {
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
		case errors.Is(err, botflota.ErrNotFound):
			account = nil
		default:
			return nil, err
		}
		stored, err := s.pairings.Get(ctx, scope, bot)
		switch {
		case err == nil:
		case errors.Is(err, botflota.ErrNotFound):
			stored = nil
		default:
			return nil, err
		}
		state, err := s.pairingState(ctx, account, stored)
		if err != nil {
			return nil, err
		}
		view.Pairing = string(state)
		if state == botflota.PairingPaired && account != nil {
			view.Phone = account.Phone()
		}
		if stored != nil && stored.State == state && !stored.LastChange.IsZero() {
			changed := stored.LastChange
			view.LastChange = &changed
		}
		bots = append(bots, view)
	}
	return bots, nil
}

// pairingState prefers the live credential check when fleet run injected one;
// without it the stored pairing state is the instance's view. The phone number
// of a bot is shown only while it is paired, so an unpaired link reports no
// number (ADR 0011, ADR 0039).
func (s *Server) pairingState(
	ctx context.Context, account *botflota.LinkedAccount,
	stored *botflota.PairingStatus,
) (botflota.PairingState, error) {
	if account == nil {
		return botflota.PairingNotPaired, nil
	}
	if s.presence != nil {
		ready, err := s.presence.HasCredentials(ctx, account)
		if err != nil {
			return "", err
		}
		if !ready {
			return botflota.PairingUnpaired, nil
		}
		return botflota.PairingPaired, nil
	}
	if stored == nil {
		return botflota.PairingNotPaired, nil
	}
	return stored.State, nil
}

func (s *Server) activeSessions(ctx context.Context, scope, bot string) (int, error) {
	return s.sessions.CountActive(ctx, scope, bot, time.Now())
}
