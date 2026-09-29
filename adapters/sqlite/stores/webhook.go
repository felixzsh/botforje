package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

// WebhookStore is the durable outbound webhook queue: one row per event and
// target, with the raw payload and the outcome of every attempt (ADR 0045).
type WebhookStore struct{ db *sql.DB }

func NewWebhookStore(db *sql.DB) *WebhookStore { return &WebhookStore{db: db} }

// Enqueue adds one delivery. The same event for the same target is already
// queued or delivered, so a repeat stores nothing and reports success: the
// event id is the consumer's dedup key (ADR 0045).
func (s *WebhookStore) Enqueue(
	ctx context.Context, delivery *botflota.WebhookDelivery,
) error {
	if delivery == nil {
		return errors.New("sqlite: webhook delivery is nil")
	}
	if delivery.EventID == "" || delivery.URL == "" {
		return errors.New("sqlite: webhook delivery has no event or url")
	}
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO webhook_deliveries (event_id, delivery_id, url, event,
		 scope, payload, state, attempts, next_attempt, error, created_at,
		 updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, '', '', ?, ?)
		 ON CONFLICT (event_id, url) DO NOTHING`,
		delivery.EventID, delivery.DeliveryID, delivery.URL, delivery.Event,
		delivery.Scope, string(delivery.Payload), now, now)
	if err != nil {
		return fmt.Errorf("sqlite: enqueue webhook: %w", err)
	}
	return nil
}

// NextPending returns the pending deliveries due at a time, oldest first.
// A scheduled retry does not block the rows behind it: unlike the per-bot
// outbox, webhook deliveries have no shared pacing (ADR 0045).
func (s *WebhookStore) NextPending(
	ctx context.Context, at time.Time, limit int,
) ([]botflota.WebhookDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	const query = `SELECT id, event_id, delivery_id, url, event, scope, payload,
		attempts, next_attempt
		FROM webhook_deliveries
		WHERE state = 'pending' AND next_attempt <= ?
		ORDER BY id LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, formatTime(at), limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: webhook next: %w", err)
	}
	defer rows.Close()
	var deliveries []botflota.WebhookDelivery
	for rows.Next() {
		var (
			delivery    botflota.WebhookDelivery
			payload     string
			nextAttempt string
		)
		if err := rows.Scan(&delivery.ID, &delivery.EventID, &delivery.DeliveryID,
			&delivery.URL, &delivery.Event, &delivery.Scope, &payload,
			&delivery.Attempts, &nextAttempt); err != nil {
			return nil, fmt.Errorf("sqlite: webhook scan: %w", err)
		}
		delivery.Payload = []byte(payload)
		delivery.NextAttempt = parseTime(nextAttempt)
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

// MarkSent records a delivery the target accepted.
func (s *WebhookStore) MarkSent(ctx context.Context, id int64, at time.Time) error {
	return s.updatePending(ctx, id,
		`UPDATE webhook_deliveries SET state = 'sent', attempts = attempts + 1,
		 error = '', updated_at = ? WHERE id = ? AND state = 'pending'`,
		formatTime(at), id)
}

// MarkRetry keeps a delivery pending and schedules its next attempt.
func (s *WebhookStore) MarkRetry(
	ctx context.Context, id int64, reason string, next, at time.Time,
) error {
	return s.updatePending(ctx, id,
		`UPDATE webhook_deliveries SET attempts = attempts + 1,
		 next_attempt = ?, error = ?, updated_at = ?
		 WHERE id = ? AND state = 'pending'`,
		formatTime(next), reason, formatTime(at), id)
}

// MarkFailed gives up on a delivery for good; the row stays for diagnosis.
func (s *WebhookStore) MarkFailed(
	ctx context.Context, id int64, reason string, at time.Time,
) error {
	return s.updatePending(ctx, id,
		`UPDATE webhook_deliveries SET state = 'failed', attempts = attempts + 1,
		 error = ?, updated_at = ? WHERE id = ? AND state = 'pending'`,
		reason, formatTime(at), id)
}

func (s *WebhookStore) updatePending(
	ctx context.Context, id int64, query string, args ...any,
) error {
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite: webhook update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return botflota.ErrNotFound
	}
	return nil
}
