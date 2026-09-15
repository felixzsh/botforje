package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botforje/internal/domain"
)

// ChatStore persists the durable chat with each person (ADR 0026).
type ChatStore struct{ db *sql.DB }

const chatColumns = `id, scope, bot, sender, started_at, updated_at`

// Ensure returns the chat for (scope, bot, sender), creating it on first use.
func (s *ChatStore) Ensure(
	ctx context.Context, scope, bot, sender string, at time.Time,
) (*domain.Chat, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chats (scope, bot, sender, started_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (scope, bot, sender) DO NOTHING`,
		scope, bot, sender, formatTime(at), formatTime(at))
	if err != nil {
		return nil, fmt.Errorf("sqlite: ensure chat: %w", err)
	}
	var (
		chat      domain.Chat
		startedAt string
		updatedAt string
	)
	err = s.db.QueryRowContext(ctx,
		"SELECT "+chatColumns+" FROM chats WHERE scope = ? AND bot = ? AND sender = ?",
		scope, bot, sender).
		Scan(&chat.ID, &chat.Scope, &chat.Bot, &chat.Sender, &startedAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get chat: %w", err)
	}
	chat.StartedAt = parseTime(startedAt)
	chat.UpdatedAt = parseTime(updatedAt)
	return &chat, nil
}

// Touch updates the last-activity time of a chat.
func (s *ChatStore) Touch(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE chats SET updated_at = ? WHERE id = ?", formatTime(at), id)
	if err != nil {
		return fmt.Errorf("sqlite: touch chat: %w", err)
	}
	return nil
}

// SessionStore persists a chat's session runs (ADR 0026).
type SessionStore struct{ db *sql.DB }

const sessionColumns = `id, chat_id, node, memory,
	graph_context, node_context, cooldowns, created_at, updated_at, expires_at`

// Latest returns the most recent session for a chat or domain.ErrNotFound.
func (s *SessionStore) Latest(ctx context.Context, chatID int64) (*domain.Session, error) {
	query := "SELECT " + sessionColumns +
		" FROM sessions WHERE chat_id = ? ORDER BY id DESC LIMIT 1"
	var (
		session                     domain.Session
		memory, graphCtx, nodeCtx   string
		cooldowns, created, updated string
		expires                     string
	)
	err := s.db.QueryRowContext(ctx, query, chatID).Scan(
		&session.ID, &session.ChatID, &session.Node, &memory, &graphCtx, &nodeCtx,
		&cooldowns, &created, &updated, &expires,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get session: %w", err)
	}
	if err := decodeJSON(memory, &session.Memory); err != nil {
		return nil, fmt.Errorf("sqlite: session memory: %w", err)
	}
	if err := decodeJSON(graphCtx, &session.GraphContext); err != nil {
		return nil, fmt.Errorf("sqlite: graph context: %w", err)
	}
	if err := decodeJSON(nodeCtx, &session.NodeContext); err != nil {
		return nil, fmt.Errorf("sqlite: node context: %w", err)
	}
	if err := decodeJSON(cooldowns, &session.Cooldowns); err != nil {
		return nil, fmt.Errorf("sqlite: cooldowns: %w", err)
	}
	session.CreatedAt = parseTime(created)
	session.UpdatedAt = parseTime(updated)
	session.ExpiresAt = parseTime(expires)
	return &session, nil
}

// Save inserts a new session run (ID zero) or updates the existing one.
func (s *SessionStore) Save(ctx context.Context, session *domain.Session) error {
	memory, err := encodeJSON(session.Memory)
	if err != nil {
		return fmt.Errorf("sqlite: session memory: %w", err)
	}
	graphCtx, err := encodeJSON(session.GraphContext)
	if err != nil {
		return fmt.Errorf("sqlite: graph context: %w", err)
	}
	nodeCtx, err := encodeJSON(session.NodeContext)
	if err != nil {
		return fmt.Errorf("sqlite: node context: %w", err)
	}
	cooldowns, err := encodeJSON(session.Cooldowns)
	if err != nil {
		return fmt.Errorf("sqlite: cooldowns: %w", err)
	}
	if session.ID == 0 {
		result, err := s.db.ExecContext(ctx,
			`INSERT INTO sessions (chat_id, node, memory, graph_context, node_context,
			 cooldowns, created_at, updated_at, expires_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			session.ChatID, session.Node, memory, graphCtx, nodeCtx, cooldowns,
			formatTime(session.CreatedAt), formatTime(session.UpdatedAt),
			formatTime(session.ExpiresAt))
		if err != nil {
			return fmt.Errorf("sqlite: save session: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("sqlite: session id: %w", err)
		}
		session.ID = id
		return nil
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE sessions SET node = ?, memory = ?, graph_context = ?,
		 node_context = ?, cooldowns = ?, created_at = ?, updated_at = ?,
		 expires_at = ? WHERE id = ?`,
		session.Node, memory, graphCtx, nodeCtx, cooldowns,
		formatTime(session.CreatedAt), formatTime(session.UpdatedAt),
		formatTime(session.ExpiresAt), session.ID)
	if err != nil {
		return fmt.Errorf("sqlite: save session: %w", err)
	}
	return nil
}

// Delete removes every session run of a chat; message history is kept.
func (s *SessionStore) Delete(ctx context.Context, chatID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE chat_id = ?", chatID)
	if err != nil {
		return fmt.Errorf("sqlite: delete session: %w", err)
	}
	return nil
}

// MessageStore persists message history (ADR 0026).
type MessageStore struct{ db *sql.DB }

const messageColumns = `id, chat_id, session_id, direction, kind, body, sent_at`

// Append inserts the message and sets its ID.
func (s *MessageStore) Append(ctx context.Context, message *domain.Message) error {
	var sessionID any
	if message.SessionID != 0 {
		sessionID = message.SessionID
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (chat_id, session_id, direction, kind, body, sent_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		message.ChatID, sessionID, string(message.Direction), string(message.Kind),
		message.Body, formatTime(message.At))
	if err != nil {
		return fmt.Errorf("sqlite: append message: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: message id: %w", err)
	}
	message.ID = id
	return nil
}

// List returns up to limit messages of a chat, oldest first.
func (s *MessageStore) List(
	ctx context.Context, chatID int64, limit int,
) ([]domain.Message, error) {
	const query = `SELECT ` + messageColumns +
		` FROM messages WHERE chat_id = ? ORDER BY id LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list messages: %w", err)
	}
	defer rows.Close()
	var messages []domain.Message
	for rows.Next() {
		var (
			message         domain.Message
			sessionID       sql.NullInt64
			direction, kind string
			sentAt          string
		)
		if err := rows.Scan(&message.ID, &message.ChatID, &sessionID,
			&direction, &kind, &message.Body, &sentAt); err != nil {
			return nil, err
		}
		if sessionID.Valid {
			message.SessionID = sessionID.Int64
		}
		message.Direction = domain.Direction(direction)
		message.Kind = domain.OutgoingKind(kind)
		message.At = parseTime(sentAt)
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// AccountStore persists paired channel accounts (ADR 0006).
type AccountStore struct{ db *sql.DB }

// Get loads the paired account for (scope, bot) or returns domain.ErrNotFound.
func (s *AccountStore) Get(ctx context.Context, scope, bot string) (*domain.Account, error) {
	var account domain.Account
	err := s.db.QueryRowContext(ctx,
		"SELECT scope, bot, jid FROM accounts WHERE scope = ? AND bot = ?", scope, bot).
		Scan(&account.Scope, &account.Bot, &account.JID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get account: %w", err)
	}
	return &account, nil
}

// List returns all paired accounts in stable order.
func (s *AccountStore) List(ctx context.Context) ([]domain.Account, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT scope, bot, jid FROM accounts ORDER BY scope, bot")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list accounts: %w", err)
	}
	defer rows.Close()
	var accounts []domain.Account
	for rows.Next() {
		var account domain.Account
		if err := rows.Scan(&account.Scope, &account.Bot, &account.JID); err != nil {
			return nil, fmt.Errorf("sqlite: scan account: %w", err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list accounts: %w", err)
	}
	return accounts, nil
}

// Put inserts or replaces the paired account.
func (s *AccountStore) Put(ctx context.Context, account *domain.Account) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO accounts (scope, bot, jid) VALUES (?, ?, ?)
		 ON CONFLICT (scope, bot) DO UPDATE SET jid = excluded.jid`,
		account.Scope, account.Bot, account.JID)
	if err != nil {
		return fmt.Errorf("sqlite: put account: %w", err)
	}
	return nil
}

// Delete removes the paired account for (scope, bot).
func (s *AccountStore) Delete(ctx context.Context, scope, bot string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM accounts WHERE scope = ? AND bot = ?", scope, bot)
	if err != nil {
		return fmt.Errorf("sqlite: delete account: %w", err)
	}
	return nil
}

// GetPairingStatus loads the last persisted pairing transition.
func (s *AccountStore) GetPairingStatus(
	ctx context.Context, scope, bot string,
) (*domain.PairingStatus, error) {
	var status domain.PairingStatus
	var changed string
	err := s.db.QueryRowContext(ctx,
		"SELECT scope, bot, state, last_change FROM pairing_status "+
			"WHERE scope = ? AND bot = ?", scope, bot).
		Scan(&status.Scope, &status.Bot, &status.State, &changed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get pairing status: %w", err)
	}
	status.LastChange = parseTime(changed)
	return &status, nil
}

// SetPairingStatus stores the last pairing transition for a bot.
func (s *AccountStore) SetPairingStatus(
	ctx context.Context, status *domain.PairingStatus,
) error {
	if status == nil {
		return errors.New("sqlite: nil pairing status")
	}
	if status.LastChange.IsZero() {
		return errors.New("sqlite: pairing status has no last change")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO pairing_status (scope, bot, state, last_change) VALUES (?, ?, ?, ?)
		 ON CONFLICT (scope, bot) DO UPDATE SET state = excluded.state,
		 last_change = excluded.last_change`,
		status.Scope, status.Bot, status.State, formatTime(status.LastChange))
	if err != nil {
		return fmt.Errorf("sqlite: set pairing status: %w", err)
	}
	return nil
}

func encodeJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeJSON(data string, out any) error {
	if data == "" {
		return nil
	}
	return json.Unmarshal([]byte(data), out)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
