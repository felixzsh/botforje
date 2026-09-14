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

// SessionStore persists sessions (ADR 0006).
type SessionStore struct{ db *sql.DB }

const sessionColumns = `bot, sender, sender_lid, scope, node, memory,
	graph_context, node_context, cooldowns, created_at, updated_at, expires_at`

// Get loads the session for (scope, bot, sender) or returns domain.ErrNotFound.
func (s *SessionStore) Get(
	ctx context.Context, scope, bot, sender string,
) (*domain.Session, error) {
	query := "SELECT " + sessionColumns +
		" FROM sessions WHERE scope = ? AND bot = ? AND sender = ?"
	var (
		session                     domain.Session
		memory, graphCtx, nodeCtx   string
		cooldowns, created, updated string
		expires                     string
	)
	err := s.db.QueryRowContext(ctx, query, scope, bot, sender).Scan(
		&session.Bot, &session.Sender, &session.SenderLID, &session.Scope,
		&session.Node, &memory, &graphCtx, &nodeCtx, &cooldowns,
		&created, &updated, &expires,
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

// Save inserts or replaces the session.
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
	query := `INSERT INTO sessions (` + sessionColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope, bot, sender) DO UPDATE SET
			sender_lid = excluded.sender_lid,
			scope = excluded.scope,
			node = excluded.node,
			memory = excluded.memory,
			graph_context = excluded.graph_context,
			node_context = excluded.node_context,
			cooldowns = excluded.cooldowns,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			expires_at = excluded.expires_at`
	_, err = s.db.ExecContext(ctx, query,
		session.Bot, session.Sender, session.SenderLID, session.Scope, session.Node,
		memory, graphCtx, nodeCtx, cooldowns,
		formatTime(session.CreatedAt), formatTime(session.UpdatedAt),
		formatTime(session.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("sqlite: save session: %w", err)
	}
	return nil
}

// Delete removes the session for (scope, bot, sender); a missing row is not an
// error.
func (s *SessionStore) Delete(ctx context.Context, scope, bot, sender string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE scope = ? AND bot = ? AND sender = ?", scope, bot, sender)
	if err != nil {
		return fmt.Errorf("sqlite: delete session: %w", err)
	}
	return nil
}

// ConversationStore persists conversation threads (ADR 0006).
type ConversationStore struct{ db *sql.DB }

// Start inserts the conversation and sets its ID.
func (s *ConversationStore) Start(ctx context.Context, conversation *domain.Conversation) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO conversations (bot, sender, scope, started_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		conversation.Bot, conversation.Sender, conversation.Scope,
		formatTime(conversation.StartedAt), formatTime(conversation.UpdatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: start conversation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: conversation id: %w", err)
	}
	conversation.ID = id
	return nil
}

// Touch updates the last-activity time of a conversation.
func (s *ConversationStore) Touch(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET updated_at = ? WHERE id = ?", formatTime(at), id)
	if err != nil {
		return fmt.Errorf("sqlite: touch conversation: %w", err)
	}
	return nil
}

// MessageStore persists message history (ADR 0006).
type MessageStore struct{ db *sql.DB }

// Append inserts the message and sets its ID.
func (s *MessageStore) Append(ctx context.Context, message *domain.Message) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, direction, kind, body, sent_at)
		 VALUES (?, ?, ?, ?, ?)`,
		message.ConversationID, string(message.Direction), string(message.Kind),
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

// List returns up to limit messages of a conversation, oldest first.
func (s *MessageStore) List(
	ctx context.Context, conversationID int64, limit int,
) ([]domain.Message, error) {
	const query = `SELECT id, conversation_id, direction, kind, body, sent_at
		FROM messages WHERE conversation_id = ? ORDER BY id LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list messages: %w", err)
	}
	defer rows.Close()
	var messages []domain.Message
	for rows.Next() {
		var (
			message         domain.Message
			direction, kind string
			sentAt          string
		)
		if err := rows.Scan(&message.ID, &message.ConversationID, &direction, &kind,
			&message.Body, &sentAt); err != nil {
			return nil, err
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
