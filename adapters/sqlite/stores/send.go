package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/felixzsh/botflota"
)

// SendStore commits one API-origin outbound message outside a turn: the
// contact identity, the chat, the message, the chat's activity, and the queued
// delivery in one transaction (ADR 0044).
type SendStore struct{ db *sql.DB }

func NewSendStore(db *sql.DB) *SendStore { return &SendStore{db: db} }

// FindSend returns the send this bot already accepted under a client id.
// An unknown key reports ErrNotFound, and an empty key is rejected: only a
// real admission key reconciles (ADR 0044).
func (s *SendStore) FindSend(
	ctx context.Context, scope, bot, clientID string,
) (botflota.SendResult, error) {
	if clientID == "" {
		return botflota.SendResult{}, botflota.ErrNotFound
	}
	var result botflota.SendResult
	err := s.db.QueryRowContext(ctx,
		`SELECT id, message_id FROM outbox
		 WHERE scope = ? AND bot = ? AND client_id = ?`,
		scope, bot, clientID).Scan(&result.OutboxID, &result.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return botflota.SendResult{}, botflota.ErrNotFound
	}
	if err != nil {
		return botflota.SendResult{}, fmt.Errorf("sqlite: find accepted send: %w", err)
	}
	return result, nil
}

func (s *SendStore) CommitSend(
	ctx context.Context, send botflota.SendRecord,
) (botflota.SendResult, error) {
	if send.Scope == "" || send.Bot == "" {
		return botflota.SendResult{}, errors.New("sqlite: send has no scope or bot")
	}
	if !send.Target.Valid() {
		return botflota.SendResult{}, errors.New("sqlite: send has no target")
	}
	if send.Content == nil {
		return botflota.SendResult{}, errors.New("sqlite: send has no content")
	}
	if send.Recipient != nil && !send.Recipient.Valid() {
		return botflota.SendResult{}, errors.New("sqlite: send recipient is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return botflota.SendResult{}, fmt.Errorf("sqlite: begin send: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var peerID int64
	if send.Recipient != nil {
		peers := PeerStore{db: tx}
		peer, err := peers.EnsurePeer(ctx, send.Scope, *send.Recipient, send.At)
		if err != nil {
			return botflota.SendResult{}, err
		}
		peerID = peer.ID
	}
	chats := ChatStore{db: tx}
	chat, err := chats.Ensure(ctx, send.Scope, send.Bot, send.Target, peerID, send.At)
	if err != nil {
		return botflota.SendResult{}, err
	}
	messages := MessageStore{db: tx}
	message := &botflota.Message{
		ChatID:    chat.ID,
		Target:    send.Target,
		Direction: botflota.Outbound,
		Content:   send.Content,
		At:        send.At,
		Ingest:    botflota.IngestAPI,
		Origin:    botflota.OriginAPI,
		Routing:   botflota.RoutingUnknown,
	}
	if err := messages.Append(ctx, message); err != nil {
		return botflota.SendResult{}, fmt.Errorf("sqlite: store sent message: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (scope, bot, message_id, target_provider, target_kind,
		 target_thread, target_topic, client_id, binding_generation, delivery_key,
		 state, attempts, next_attempt, error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', 0, '', '', ?, ?)`,
		send.Scope, send.Bot, message.ID, send.Target.Provider,
		string(send.Target.Kind), send.Target.Thread, send.Target.Topic,
		send.ClientID, send.BindingGeneration, newDeliveryKey(),
		formatTime(send.At), formatTime(send.At))
	if err != nil {
		_ = tx.Rollback()
		if send.ClientID != "" {
			existing, found, lookupErr := s.acceptedSend(ctx, send)
			if lookupErr != nil {
				return botflota.SendResult{}, lookupErr
			}
			if found {
				return existing, botflota.ErrDuplicate
			}
		}
		return botflota.SendResult{}, fmt.Errorf("sqlite: queue send: %w", err)
	}
	outboxID, err := result.LastInsertId()
	if err != nil {
		return botflota.SendResult{}, fmt.Errorf("sqlite: send queue id: %w", err)
	}
	// An outbound moves the chat's activity without counting as an inbound
	// message (ADR 0026, ADR 0044).
	if _, err := tx.ExecContext(ctx,
		"UPDATE chats SET updated_at = ? WHERE id = ?",
		formatTime(send.At), chat.ID); err != nil {
		return botflota.SendResult{}, fmt.Errorf("sqlite: move chat activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return botflota.SendResult{}, fmt.Errorf("sqlite: commit send: %w", err)
	}
	committed = true
	return botflota.SendResult{MessageID: message.ID, OutboxID: outboxID}, nil
}

// acceptedSend finds the send this bot already accepted under the same client
// id, so a repeat answers with the message it stored (ADR 0044).
func (s *SendStore) acceptedSend(
	ctx context.Context, send botflota.SendRecord,
) (botflota.SendResult, bool, error) {
	var result botflota.SendResult
	err := s.db.QueryRowContext(ctx,
		`SELECT id, message_id FROM outbox
		 WHERE scope = ? AND bot = ? AND client_id = ?`,
		send.Scope, send.Bot, send.ClientID).
		Scan(&result.OutboxID, &result.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return botflota.SendResult{}, false, nil
	}
	if err != nil {
		return botflota.SendResult{}, false,
			fmt.Errorf("sqlite: find accepted send: %w", err)
	}
	return result, true, nil
}
