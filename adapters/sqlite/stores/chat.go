package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

type ChatStore struct{ db *sql.DB }

func NewChatStore(db *sql.DB) *ChatStore { return &ChatStore{db: db} }

const chatColumns = `id, scope, bot, sender_id, started_at, updated_at, message_count, guards`

func (s *ChatStore) Ensure(
	ctx context.Context, scope, bot string, senderID int64, at time.Time,
) (*botflota.Chat, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chats (scope, bot, sender_id, started_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (scope, bot, sender_id) DO NOTHING`,
		scope, bot, senderID, formatTime(at), formatTime(at))
	if err != nil {
		return nil, fmt.Errorf("sqlite: ensure chat: %w", err)
	}
	chat, err := s.ForSender(ctx, scope, bot, senderID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ensure chat: %w", err)
	}
	return chat, nil
}

func (s *ChatStore) ForSender(
	ctx context.Context, scope, bot string, senderID int64,
) (*botflota.Chat, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+chatColumns+" FROM chats WHERE scope = ? AND bot = ? AND sender_id = ?",
		scope, bot, senderID)
	chat, err := scanChat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get chat: %w", err)
	}
	return chat, nil
}

// Recent returns up to limit chats of a scope, newest first. An empty bot
// means every bot in the scope; a zero limit means every chat: SQLite reads a
// negative LIMIT as no bound, which is what `chat sync --all` walks.
func (s *ChatStore) Recent(
	ctx context.Context, scope, bot string, limit int,
) ([]botflota.ChatSummary, error) {
	if limit <= 0 {
		limit = -1
	}
	const query = `SELECT c.id, c.scope, c.bot, c.sender_id, c.started_at,
			c.updated_at, c.message_count, c.guards, s.phone, s.display_name,
			COALESCE(m.total, 0), COALESCE(m.sync, 0), COALESCE(m.live, 0)
		FROM chats c JOIN senders s ON s.id = c.sender_id
		LEFT JOIN (
			SELECT chat_id, COUNT(*) AS total,
			       SUM(ingest_kind = 'sync') AS sync,
			       SUM(ingest_kind = 'live') AS live
			FROM messages GROUP BY chat_id
		) m ON m.chat_id = c.id
		WHERE c.scope = ? AND (? = '' OR c.bot = ?)
		ORDER BY c.updated_at DESC, c.id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, scope, bot, bot, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list chats: %w", err)
	}
	defer rows.Close()
	var chats []botflota.ChatSummary
	for rows.Next() {
		var (
			summary              botflota.ChatSummary
			startedAt, updatedAt string
			guards               string
		)
		if err := rows.Scan(&summary.ID, &summary.Scope, &summary.Bot,
			&summary.SenderID, &startedAt, &updatedAt, &summary.MessageCount,
			&guards, &summary.Phone, &summary.DisplayName,
			&summary.Messages, &summary.Sync, &summary.Live); err != nil {
			return nil, err
		}
		summary.StartedAt = parseTime(startedAt)
		summary.UpdatedAt = parseTime(updatedAt)
		if err := decodeJSON(guards, &summary.Guards); err != nil {
			return nil, fmt.Errorf("sqlite: guards: %w", err)
		}
		chats = append(chats, summary)
	}
	return chats, rows.Err()
}

func scanChat(row interface{ Scan(...any) error }) (*botflota.Chat, error) {
	var (
		chat                 botflota.Chat
		startedAt, updatedAt string
		guards               string
	)
	if err := row.Scan(&chat.ID, &chat.Scope, &chat.Bot, &chat.SenderID,
		&startedAt, &updatedAt, &chat.MessageCount, &guards); err != nil {
		return nil, err
	}
	chat.StartedAt = parseTime(startedAt)
	chat.UpdatedAt = parseTime(updatedAt)
	if err := decodeJSON(guards, &chat.Guards); err != nil {
		return nil, fmt.Errorf("sqlite: guards: %w", err)
	}
	return &chat, nil
}

func (s *ChatStore) RecordMessage(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE chats SET updated_at = ?, message_count = message_count + 1 WHERE id = ?",
		formatTime(at), id)
	if err != nil {
		return fmt.Errorf("sqlite: record message: %w", err)
	}
	return nil
}

// NoteHistory widens a chat's activity to cover imported history without
// touching the live inbound count (ADR 0035). Comparisons run on the stored
// RFC 3339 text, whose order is correct here.
func (s *ChatStore) NoteHistory(
	ctx context.Context, chatID int64, from, to time.Time,
) error {
	if from.IsZero() {
		from = to
	}
	if to.IsZero() {
		to = from
	}
	if from.IsZero() {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE chats SET
		   started_at = CASE
		     WHEN started_at = '' OR started_at > ? THEN ? ELSE started_at END,
		   updated_at = CASE WHEN updated_at < ? THEN ? ELSE updated_at END
		 WHERE id = ?`,
		formatTime(from), formatTime(from), formatTime(to), formatTime(to), chatID)
	if err != nil {
		return fmt.Errorf("sqlite: note chat history: %w", err)
	}
	return nil
}

func (s *ChatStore) SaveGuards(
	ctx context.Context, chatID int64, guards map[string]botflota.GuardState,
) error {
	data, err := encodeJSON(guards)
	if err != nil {
		return fmt.Errorf("sqlite: guards: %w", err)
	}
	_, err = s.db.ExecContext(ctx, "UPDATE chats SET guards = ? WHERE id = ?", data, chatID)
	if err != nil {
		return fmt.Errorf("sqlite: save guards: %w", err)
	}
	return nil
}
