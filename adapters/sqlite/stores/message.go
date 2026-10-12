package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/felixzsh/botflota"
)

type MessageStore struct{ db dbtx }

func NewMessageStore(db *sql.DB) *MessageStore { return &MessageStore{db: db} }

const messageColumns = `id, chat_id, session_id, target_provider, target_kind,
	target_thread, target_topic, direction, kind, body, content,
	sent_at, provider_message_id, ingest_kind, origin, routing`

func (s *MessageStore) Append(ctx context.Context, message *botflota.Message) error {
	if message.Content == nil {
		return errors.New("sqlite: message has no content")
	}
	if !message.Target.Valid() {
		return errors.New("sqlite: message has no destination target")
	}
	// The owner comes from the chat, so a message is deduplicated inside the
	// bot that stores it, never against another bot's identical destination
	// (ADR 0051).
	scope, bot, err := s.chatOwner(ctx, message.ChatID)
	if err != nil {
		return err
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
		`INSERT INTO messages (chat_id, scope, bot, session_id, target_provider,
		 target_kind, target_thread, target_topic, direction, kind, body, content,
		 sent_at, provider_message_id, ingest_kind, origin, routing)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT DO NOTHING`,
		message.ChatID, scope, bot, sessionID, message.Target.Provider,
		string(message.Target.Kind), message.Target.Thread, message.Target.Topic,
		string(message.Direction), string(message.Content.MessageKind()),
		message.Content.VisibleText(), string(content), formatTime(message.At),
		message.ProviderMessageID, string(message.Ingest), string(message.Origin),
		string(message.Routing))
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

// chatOwner reads the scope and bot that own a chat, so a stored message is
// deduplicated inside its owner, never across bots (ADR 0051).
func (s *MessageStore) chatOwner(
	ctx context.Context, chatID int64,
) (string, string, error) {
	var scope, bot string
	err := s.db.QueryRowContext(ctx,
		"SELECT scope, bot FROM chats WHERE id = ?", chatID).Scan(&scope, &bot)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("%w: chat %d", botflota.ErrNotFound, chatID)
	}
	if err != nil {
		return "", "", fmt.Errorf("sqlite: message chat owner: %w", err)
	}
	return scope, bot, nil
}

// HasProviderID reports whether this bot already stored a message to this
// destination with this channel id; an empty id never matches. Both the
// destination and the owner scope the id, so an alternate recipient's ids
// never collide with the chat whose history records the send, and one bot
// never sees another's (ADR 0051).
func (s *MessageStore) HasProviderID(
	ctx context.Context, scope, bot string, target botflota.ChatTarget,
	providerID string,
) (bool, error) {
	if providerID == "" {
		return false, nil
	}
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM messages
		 WHERE scope = ? AND bot = ?
		 AND target_provider = ? AND target_kind = ? AND target_thread = ?
		 AND target_topic = ? AND provider_message_id = ?)`,
		scope, bot, target.Provider, string(target.Kind), target.Thread,
		target.Topic, providerID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("sqlite: find provider id: %w", err)
	}
	return exists, nil
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
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// scanMessage reads one row of messageColumns, so a caller that streams rows
// decodes each message the same way a caller that collects them does.
func scanMessage(rows *sql.Rows) (botflota.Message, error) {
	var (
		message                       botflota.Message
		sessionID                     sql.NullInt64
		targetKind                    string
		direction, kind, body, ingest string
		content, origin, routing      string
		sentAt                        string
	)
	if err := rows.Scan(&message.ID, &message.ChatID, &sessionID,
		&message.Target.Provider, &targetKind, &message.Target.Thread,
		&message.Target.Topic, &direction, &kind, &body, &content, &sentAt,
		&message.ProviderMessageID, &ingest, &origin, &routing); err != nil {
		return botflota.Message{}, err
	}
	message.Target.Kind = botflota.ChatKind(targetKind)
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
		return botflota.Message{}, fmt.Errorf("sqlite: decode message content: %w", err)
	}
	message.Content = decoded
	return message, nil
}
