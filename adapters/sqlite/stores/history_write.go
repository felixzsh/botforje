package stores

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

// DeleteBot removes one bot's chats — and with them, by cascade, their sessions,
// messages, and queued replies — plus its address book. Contact identities
// belong to the scope and stay (ADR 0029). One transaction: a crash leaves all
// of it or none of it.
func (s *HistoryStore) DeleteBot(
	ctx context.Context, scope, bot string,
) (botflota.HistoryCounts, error) {
	var counts botflota.HistoryCounts
	err := s.inTransaction(ctx, func(tx *sql.Tx) error {
		counted, err := countHistory(ctx, tx, scope, bot)
		if err != nil {
			return err
		}
		counts = counted
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM chats WHERE scope = ? AND bot = ?`, scope, bot); err != nil {
			return fmt.Errorf("sqlite: delete chats: %w", err)
		}
		result, err := tx.ExecContext(ctx,
			`DELETE FROM bot_contacts WHERE scope = ? AND bot = ?`, scope, bot)
		if err != nil {
			return fmt.Errorf("sqlite: delete address book: %w", err)
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("sqlite: delete address book: %w", err)
		}
		counts.AddressBook = removed
		return nil
	})
	return counts, err
}

// MoveBot re-keys one bot's chats and address book to another bot of the same scope.
// Nothing is merged: the destination must hold no chat, so every moved chat
// keeps its start, activity, guards, and session runs and lands conflict-free.
// The source's queued replies are removed rather than carried, because the
// message they belong to now belongs to another bot. A run still active at the
// source is expired, so the destination never looks like it resumes another
// bot's graph (ADR 0042).
func (s *HistoryStore) MoveBot(
	ctx context.Context, scope, from, to string, at time.Time,
) (botflota.HistoryCounts, error) {
	var counts botflota.HistoryCounts
	err := s.inTransaction(ctx, func(tx *sql.Tx) error {
		counted, err := countHistory(ctx, tx, scope, from)
		if err != nil {
			return err
		}
		counts = counted
		// A source with nothing left is a repeat of a migration that already
		// landed: it reports zero rather than finding its own history at the
		// destination and refusing itself (ADR 0042: a retry needs no key).
		if counts.Chats == 0 && counts.AddressBook == 0 {
			return nil
		}
		occupied, err := destinationHasChats(ctx, tx, scope, to)
		if err != nil {
			return err
		}
		if occupied {
			return fmt.Errorf("%w: %s/%s", botflota.ErrDestinationNotEmpty, scope, to)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM outbox WHERE scope = ? AND bot = ?`, scope, from); err != nil {
			return fmt.Errorf("sqlite: clear source queue: %w", err)
		}
		// The parentheses matter: without them the OR would let an empty expiry
		// on another bot's session match, expiring runs the move never touches.
		if _, err := tx.ExecContext(ctx,
			`UPDATE sessions SET expires_at = ?
			 WHERE (expires_at = '' OR expires_at > ?)
			   AND chat_id IN (SELECT id FROM chats WHERE scope = ? AND bot = ?)`,
			formatTime(at), formatTime(at), scope, from); err != nil {
			return fmt.Errorf("sqlite: expire source sessions: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE chats SET bot = ? WHERE scope = ? AND bot = ?`,
			to, scope, from); err != nil {
			return fmt.Errorf("sqlite: move chats: %w", err)
		}
		// A phone the destination already knows keeps the destination's row: its
		// own address book is the newer truth about that person (ADR 0037).
		result, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO bot_contacts
				(scope, bot, phone, name, push_name, business_name, updated_at)
			 SELECT scope, ?, phone, name, push_name, business_name, updated_at
			 FROM bot_contacts WHERE scope = ? AND bot = ?`, to, scope, from)
		if err != nil {
			return fmt.Errorf("sqlite: move address book: %w", err)
		}
		moved, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("sqlite: move address book: %w", err)
		}
		counts.AddressBook = moved
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM bot_contacts WHERE scope = ? AND bot = ?`, scope, from); err != nil {
			return fmt.Errorf("sqlite: delete moved address book: %w", err)
		}
		return nil
	})
	return counts, err
}

func destinationHasChats(
	ctx context.Context, tx *sql.Tx, scope, bot string,
) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM chats WHERE scope = ? AND bot = ?)`,
		scope, bot).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("sqlite: check destination: %w", err)
	}
	return exists, nil
}

// countHistory reports what one bot holds, before an operation changes it.
func countHistory(
	ctx context.Context, tx *sql.Tx, scope, bot string,
) (botflota.HistoryCounts, error) {
	var counts botflota.HistoryCounts
	err := tx.QueryRowContext(ctx,
		`SELECT
			(SELECT COUNT(*) FROM chats WHERE scope = ? AND bot = ?),
			(SELECT COUNT(*) FROM sessions WHERE chat_id IN
				(SELECT id FROM chats WHERE scope = ? AND bot = ?)),
			(SELECT COUNT(*) FROM messages WHERE chat_id IN
				(SELECT id FROM chats WHERE scope = ? AND bot = ?))`,
		scope, bot, scope, bot, scope, bot,
	).Scan(&counts.Chats, &counts.Sessions, &counts.Messages)
	if err != nil {
		return botflota.HistoryCounts{}, fmt.Errorf("sqlite: count history: %w", err)
	}
	return counts, nil
}

// inTransaction runs one history operation as a unit, so the readers of
// ReadHistory never see it half-applied and a crash leaves it all or nothing.
func (s *HistoryStore) inTransaction(
	ctx context.Context, work func(*sql.Tx) error,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin history write: %w", err)
	}
	if err := work(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit history write: %w", err)
	}
	return nil
}
