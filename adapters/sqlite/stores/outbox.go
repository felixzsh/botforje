package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

type OutboxStore struct{ db *sql.DB }

func NewOutboxStore(db *sql.DB) *OutboxStore { return &OutboxStore{db: db} }

// NextPending returns the oldest due replies of one bot. An empty
// next_attempt is due immediately, so a freshly queued reply never waits on
// a clock the turn did not set.
func (s *OutboxStore) NextPending(
	ctx context.Context, scope, bot string, now time.Time, limit int,
) ([]botflota.OutboxEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	const query = `SELECT o.id, o.attempts, o.recipient,
		m.kind, m.body, m.content, m.origin, m.routing
		FROM outbox o JOIN messages m ON m.id = o.message_id
		WHERE o.scope = ? AND o.bot = ? AND o.state = 'pending'
			AND (o.next_attempt = '' OR o.next_attempt <= ?)
		ORDER BY o.id LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, scope, bot, formatTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: outbox next: %w", err)
	}
	defer rows.Close()
	var entries []botflota.OutboxEntry
	for rows.Next() {
		var (
			entry      botflota.OutboxEntry
			kind, body string
			content    string
			origin     string
			routing    string
		)
		if err := rows.Scan(&entry.ID, &entry.Attempts, &entry.Recipient,
			&kind, &body, &content, &origin, &routing); err != nil {
			return nil, fmt.Errorf("sqlite: outbox scan: %w", err)
		}
		decoded, err := botflota.ContentFromJSON(
			botflota.MessageKind(kind), body, []byte(content))
		if err != nil {
			return nil, fmt.Errorf("sqlite: outbox decode: %w", err)
		}
		sendable, ok := decoded.(botflota.SendableContent)
		if !ok {
			return nil, fmt.Errorf(
				"sqlite: queued reply %d is not sendable (%s)", entry.ID, kind)
		}
		entry.Origin = botflota.MessageOrigin(origin)
		entry.Routing = botflota.RoutingMethod(routing)
		entry.Content = sendable
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// MarkSent records the delivery and the channel's own message id in one
// transaction; UPDATE OR IGNORE keeps a duplicate id the history sync already
// stored from failing a send that really happened.
func (s *OutboxStore) MarkSent(
	ctx context.Context, id int64, providerID string, at time.Time,
) error {
	var messageID int64
	err := s.db.QueryRowContext(ctx,
		"SELECT message_id FROM outbox WHERE id = ?", id).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return botflota.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: outbox message id: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: outbox mark sent: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx,
		`UPDATE outbox SET state = 'sent', attempts = attempts + 1,
		 error = '', updated_at = ? WHERE id = ? AND state = 'pending'`,
		formatTime(at), id)
	if err != nil {
		return fmt.Errorf("sqlite: outbox mark sent: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		return botflota.ErrNotFound
	}
	if providerID != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE OR IGNORE messages SET provider_message_id = ?
			 WHERE id = ?`, providerID, messageID); err != nil {
			return fmt.Errorf("sqlite: outbox provider id: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: outbox mark sent: %w", err)
	}
	return nil
}

// MarkRetry keeps the reply pending and schedules its next attempt.
func (s *OutboxStore) MarkRetry(
	ctx context.Context, id int64, reason string, nextAttempt, at time.Time,
) error {
	return s.updatePending(ctx, id,
		`UPDATE outbox SET attempts = attempts + 1, next_attempt = ?,
		 error = ?, updated_at = ? WHERE id = ? AND state = 'pending'`,
		formatTime(nextAttempt), reason, formatTime(at), id)
}

// MarkFailed gives up on a reply for good; the row stays for diagnosis.
func (s *OutboxStore) MarkFailed(
	ctx context.Context, id int64, reason string, at time.Time,
) error {
	return s.updatePending(ctx, id,
		`UPDATE outbox SET state = 'failed', attempts = attempts + 1,
		 error = ?, updated_at = ? WHERE id = ? AND state = 'pending'`,
		reason, formatTime(at), id)
}

func (s *OutboxStore) updatePending(
	ctx context.Context, id int64, query string, args ...any,
) error {
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite: outbox update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return botflota.ErrNotFound
	}
	return nil
}

func (s *OutboxStore) Depth(ctx context.Context, scope, bot string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox
		 WHERE scope = ? AND bot = ? AND state = 'pending'`,
		scope, bot).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite: outbox depth: %w", err)
	}
	return count, nil
}
