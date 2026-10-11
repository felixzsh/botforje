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

// peerView is the peer a chat or message is with. Phone is the legacy
// WhatsApp projection of the subject; provider and subject are the neutral
// reference (ADR 0051, ADR 0054).
type peerView struct {
	Provider string `json:"provider,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Phone    string `json:"phone"`
	Name     string `json:"name,omitempty"`
}

// contactProfileView is the scope-level identity read (ADR 0044). DisplayName
// is the best-effort push name, always present, possibly empty. FirstSeen and
// LastSeen are zero without a live inbound. The other fields derive whether
// the peer came from a directory import and whether chat/history exists.
type contactProfileView struct {
	Provider            string    `json:"provider,omitempty"`
	Subject             string    `json:"subject,omitempty"`
	Phone               string    `json:"phone"`
	DisplayName         string    `json:"display_name"`
	FirstSeen           time.Time `json:"first_seen"`
	LastSeen            time.Time `json:"last_seen"`
	AddressBookImported bool      `json:"address_book_imported"`
	HasChat             bool      `json:"has_chat"`
	HasHistory          bool      `json:"has_history"`
}

type chatView struct {
	ID        int64     `json:"id"`
	Scope     string    `json:"scope"`
	Bot       string    `json:"bot"`
	Contact   peerView  `json:"contact"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// addressBookView is one imported directory entry (ADR 0037, ADR 0051).
// Phone is the legacy WhatsApp projection of the peer subject.
type addressBookView struct {
	Scope string `json:"scope"`
	Bot   string `json:"bot"`
	// Provider and Subject are the peer the entry describes; SourceProvider and
	// SourceSubject are the account whose book supplied it.
	Provider       string    `json:"provider,omitempty"`
	Subject        string    `json:"subject,omitempty"`
	SourceProvider string    `json:"source_provider,omitempty"`
	SourceSubject  string    `json:"source_subject,omitempty"`
	Phone          string    `json:"phone"`
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
	case errors.Is(err, botflota.ErrNotFound):
		account = nil
	default:
		return botView{}, err
	}
	stored, err := s.pairings.Get(ctx, scope, bot)
	switch {
	case err == nil:
	case errors.Is(err, botflota.ErrNotFound):
		stored = nil
	default:
		return botView{}, err
	}
	state, err := s.pairingState(ctx, account, stored)
	if err != nil {
		return botView{}, err
	}
	view.Pairing = string(state)
	if state == botflota.PairingPaired && account != nil {
		view.Phone = account.Identity.Subject
	}
	if stored != nil && stored.State == state && !stored.LastChange.IsZero() {
		changed := stored.LastChange
		view.LastChange = &changed
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

// pairingState prefers the live credential check when fleet run injected one;
// without it the stored pairing state is the instance's view. The phone number
// of a bot is shown only while it is paired, so an unpaired link reports no
// number (ADR 0011, ADR 0039).
func (s *Server) pairingState(
	ctx context.Context, account *botflota.Binding,
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
