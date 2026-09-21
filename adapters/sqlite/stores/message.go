package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/felixzsh/botflota"
)

type MessageStore struct{ db *sql.DB }

func NewMessageStore(db *sql.DB) *MessageStore { return &MessageStore{db: db} }

const messageColumns = `id, chat_id, session_id, direction, kind, body, content,
	sent_at, provider_message_id, ingest_kind, origin, routing`

func (s *MessageStore) Append(ctx context.Context, message *botflota.Message) error {
	if message.Content == nil {
		return errors.New("sqlite: message has no content")
	}
	content, err := botflota.ContentJSON(message.Content)
	if err != nil {
		return fmt.Errorf("sqlite: encode message content: %w", err)
	}
	var sessionID any
	if message.SessionID != 0 {
		sessionID = message.SessionID
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (chat_id, session_id, direction, kind, body, content,
		 sent_at, provider_message_id, ingest_kind, origin, routing)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT DO NOTHING`,
		message.ChatID, sessionID, string(message.Direction),
		string(message.Content.MessageKind()), message.Content.VisibleText(),
		string(content), formatTime(message.At), message.ProviderMessageID,
		string(message.Ingest), string(message.Origin), string(message.Routing))
	if err != nil {
		return fmt.Errorf("sqlite: append message: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: append message: %w", err)
	}
	if inserted == 0 {
		return botflota.ErrDuplicate
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: message id: %w", err)
	}
	message.ID = id
	return nil
}

func (s *MessageStore) SetProviderID(ctx context.Context, id int64, providerID string) error {
	if providerID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE messages SET provider_message_id = ? WHERE id = ?", providerID, id)
	if err != nil {
		return fmt.Errorf("sqlite: set provider id: %w", err)
	}
	return nil
}

// Call rows are excluded: a call is not a position in the chat's own message
// list, so the primary device cannot page back from it. Calls stay visible in
// the read API.
func (s *MessageStore) Oldest(ctx context.Context, chatID int64) (*botflota.Message, error) {
	const query = `SELECT ` + messageColumns +
		` FROM messages WHERE chat_id = ? AND provider_message_id != ''
			AND kind != 'call'
		 ORDER BY sent_at ASC, id ASC LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, chatID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: oldest message: %w", err)
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, botflota.ErrNotFound
	}
	return &messages[0], nil
}

func (s *MessageStore) History(
	ctx context.Context, chatID, afterID int64, limit int,
) ([]botflota.Message, error) {
	if afterID > 0 {
		const query = `SELECT ` + messageColumns +
			` FROM messages WHERE chat_id = ? AND id > ? ORDER BY id LIMIT ?`
		rows, err := s.db.QueryContext(ctx, query, chatID, afterID, limit)
		if err != nil {
			return nil, fmt.Errorf("sqlite: message history: %w", err)
		}
		return scanMessages(rows)
	}
	const query = `SELECT ` + messageColumns +
		` FROM messages WHERE chat_id = ? ORDER BY id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: message history: %w", err)
	}
	messages, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	slices.Reverse(messages)
	return messages, nil
}

func scanMessages(rows *sql.Rows) ([]botflota.Message, error) {
	defer rows.Close()
	var messages []botflota.Message
	for rows.Next() {
		var (
			message                       botflota.Message
			sessionID                     sql.NullInt64
			direction, kind, body, ingest string
			content, origin, routing      string
			sentAt                        string
		)
		if err := rows.Scan(&message.ID, &message.ChatID, &sessionID,
			&direction, &kind, &body, &content, &sentAt,
			&message.ProviderMessageID, &ingest, &origin, &routing); err != nil {
			return nil, err
		}
		if sessionID.Valid {
			message.SessionID = sessionID.Int64
		}
		message.Direction = botflota.Direction(direction)
		message.Ingest = botflota.Ingest(ingest)
		message.Origin = botflota.MessageOrigin(origin)
		message.Routing = botflota.RoutingMethod(routing)
		message.At = parseTime(sentAt)
		decoded, err := botflota.ContentFromJSON(
			botflota.MessageKind(kind), body, []byte(content))
		if err != nil {
			return nil, fmt.Errorf("sqlite: decode message content: %w", err)
		}
		message.Content = decoded
		messages = append(messages, message)
	}
	return messages, rows.Err()
}
