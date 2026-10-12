package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

type ChatStore struct{ db dbtx }

func NewChatStore(db *sql.DB) *ChatStore { return &ChatStore{db: db} }

const chatColumns = `id, scope, bot, provider, kind, thread, topic, peer_id,
	started_at, updated_at, message_count, guards`

// Ensure creates or finds the chat of one complete target. A nonzero peerID
// is explicit known participant evidence: it must resolve under the same scope
// and provider, an absent reference is filled with it, and a conflicting known
// participant is rejected instead of silently rewritten (ADR 0051).
func (s *ChatStore) Ensure(
	ctx context.Context, scope, bot string, target botflota.ChatTarget,
	peerID int64, at time.Time,
) (*botflota.Chat, error) {
	if !target.Valid() {
		return nil, errors.New("sqlite: chat target needs provider, kind, and thread")
	}
	if peerID != 0 {
		if err := s.checkPeer(ctx, scope, target.Provider, peerID); err != nil {
			return nil, err
		}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chats
		   (scope, bot, provider, kind, thread, topic, peer_id, started_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (scope, bot, provider, kind, thread, topic) DO NOTHING`,
		scope, bot, target.Provider, string(target.Kind), target.Thread, target.Topic,
		nullableID(peerID), formatTime(at), formatTime(at))
	if err != nil {
		return nil, fmt.Errorf("sqlite: ensure chat: %w", err)
	}
	chat, err := s.ForTarget(ctx, scope, bot, target)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ensure chat: %w", err)
	}
	if peerID == 0 || chat.PeerID == peerID {
		return chat, nil
	}
	if chat.PeerID != 0 {
		return nil, fmt.Errorf("%w: chat %d", botflota.ErrParticipantConflict, chat.ID)
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE chats SET peer_id = ? WHERE id = ? AND peer_id IS NULL",
		peerID, chat.ID); err != nil {
		return nil, fmt.Errorf("sqlite: attach chat participant: %w", err)
	}
	chat.PeerID = peerID
	return chat, nil
}

// checkPeer rejects a participant that does not belong to the chat's scope and
// provider.
func (s *ChatStore) checkPeer(
	ctx context.Context, scope, provider string, peerID int64,
) error {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM peers
		 WHERE id = ? AND scope = ? AND provider = ?)`,
		peerID, scope, provider).Scan(&exists)
	if err != nil {
		return fmt.Errorf("sqlite: check chat participant: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: peer %d", botflota.ErrNotFound, peerID)
	}
	return nil
}

func (s *ChatStore) ForTarget(
	ctx context.Context, scope, bot string, target botflota.ChatTarget,
) (*botflota.Chat, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+chatColumns+" FROM chats WHERE scope = ? AND bot = ?"+
			" AND provider = ? AND kind = ? AND thread = ? AND topic = ?",
		scope, bot, target.Provider, string(target.Kind), target.Thread, target.Topic)
	chat, err := scanChat(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get chat: %w", err)
	}
	return chat, nil
}

// ByID reads one chat by its internal id; scope and bot checks stay with
// the caller (ADR 0044).
func (s *ChatStore) ByID(ctx context.Context, id int64) (*botflota.Chat, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+chatColumns+" FROM chats WHERE id = ?", id)
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
	const query = `SELECT c.id, c.scope, c.bot, c.provider, c.kind, c.thread,
			c.topic, c.peer_id, c.started_at, c.updated_at, c.message_count,
			c.guards, COALESCE(p.provider, ''), COALESCE(p.subject, ''),
			COALESCE(p.display_name, ''),
			COALESCE(m.total, 0), COALESCE(m.sync, 0), COALESCE(m.live, 0)
		FROM chats c LEFT JOIN peers p ON p.id = c.peer_id
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
			peerID               sql.NullInt64
		)
		if err := rows.Scan(&summary.ID, &summary.Scope, &summary.Bot,
			&summary.Target.Provider, &summary.Target.Kind, &summary.Target.Thread,
			&summary.Target.Topic, &peerID, &startedAt, &updatedAt,
			&summary.MessageCount, &guards, &summary.PeerKey.Provider,
			&summary.PeerKey.Subject, &summary.DisplayName,
			&summary.Messages, &summary.Sync, &summary.Live); err != nil {
			return nil, err
		}
		if peerID.Valid {
			summary.PeerID = peerID.Int64
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
		kind                 string
		peerID               sql.NullInt64
		startedAt, updatedAt string
		guards               string
	)
	if err := row.Scan(&chat.ID, &chat.Scope, &chat.Bot, &chat.Target.Provider,
		&kind, &chat.Target.Thread, &chat.Target.Topic, &peerID,
		&startedAt, &updatedAt, &chat.MessageCount, &guards); err != nil {
		return nil, err
	}
	chat.Target.Kind = botflota.ChatKind(kind)
	if peerID.Valid {
		chat.PeerID = peerID.Int64
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

// nullableID maps the core's zero-means-absent convention onto SQLite NULL, so
// an unknown participant is never a fabricated row (ADR 0051).
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
