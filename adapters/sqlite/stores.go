package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

// SenderStore persists the profile of each person who writes to a bot
// (ADR 0029).
type SenderStore struct{ db *sql.DB }

const senderColumns = `id, scope, phone, display_name, name_updated_at, first_seen, last_seen`

// Upsert creates the profile on first contact and updates it afterwards: it
// always moves last_seen, and adopts a non-empty name, stamping
// name_updated_at only when that name changes. The bool reports a new profile.
func (s *SenderStore) Upsert(
	ctx context.Context, scope, phone, name string, at time.Time,
) (*botflota.Sender, bool, error) {
	nameAt := ""
	if name != "" {
		nameAt = formatTime(at)
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO senders (scope, phone, display_name, name_updated_at,
		 first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (scope, phone) DO NOTHING`,
		scope, phone, name, nameAt, formatTime(at), formatTime(at))
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: upsert sender: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: upsert sender: %w", err)
	}
	created := inserted == 1
	if !created {
		_, err = s.db.ExecContext(ctx,
			`UPDATE senders SET
			   last_seen = ?,
			   display_name = CASE WHEN ? <> '' THEN ? ELSE display_name END,
			   name_updated_at = CASE
			     WHEN ? <> '' AND ? <> display_name THEN ?
			     ELSE name_updated_at END
			 WHERE scope = ? AND phone = ?`,
			formatTime(at), name, name, name, name, nameAt, scope, phone)
		if err != nil {
			return nil, false, fmt.Errorf("sqlite: update sender: %w", err)
		}
	}
	sender, err := s.Get(ctx, scope, phone)
	if err != nil {
		return nil, false, err
	}
	return sender, created, nil
}

// Get loads the profile for (scope, phone) or returns botflota.ErrNotFound.
func (s *SenderStore) Get(
	ctx context.Context, scope, phone string,
) (*botflota.Sender, error) {
	var (
		sender                      botflota.Sender
		nameAt, firstSeen, lastSeen string
	)
	err := s.db.QueryRowContext(ctx,
		"SELECT "+senderColumns+" FROM senders WHERE scope = ? AND phone = ?",
		scope, phone).
		Scan(&sender.ID, &sender.Scope, &sender.Phone, &sender.DisplayName,
			&nameAt, &firstSeen, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get sender: %w", err)
	}
	sender.NameUpdatedAt = parseTime(nameAt)
	sender.FirstSeen = parseTime(firstSeen)
	sender.LastSeen = parseTime(lastSeen)
	return &sender, nil
}

// ChatStore persists the durable chat with each person (ADR 0026, ADR 0029).
type ChatStore struct{ db *sql.DB }

const chatColumns = `id, scope, bot, sender_id, started_at, updated_at, message_count, guards`

// Ensure returns the chat for (scope, bot, sender), creating it on first use.
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

// ForSender loads the chat for (scope, bot, sender) or returns
// botflota.ErrNotFound.
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

// Recent returns up to limit chats of a scope, newest first, each with its
// sender profile. An empty bot means every bot in the scope.
func (s *ChatStore) Recent(
	ctx context.Context, scope, bot string, limit int,
) ([]botflota.ChatSummary, error) {
	const query = `SELECT c.id, c.scope, c.bot, c.sender_id, c.started_at,
			c.updated_at, c.message_count, c.guards, s.phone, s.display_name
		FROM chats c JOIN senders s ON s.id = c.sender_id
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
			&guards, &summary.Phone, &summary.DisplayName); err != nil {
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

// scanChat reads one chat row in chatColumns order.
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

// RecordMessage updates a chat's last activity and adds one inbound message.
func (s *ChatStore) RecordMessage(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE chats SET updated_at = ?, message_count = message_count + 1 WHERE id = ?",
		formatTime(at), id)
	if err != nil {
		return fmt.Errorf("sqlite: record message: %w", err)
	}
	return nil
}

// SaveGuards stores a chat's per-action guard state (ADR 0027).
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

// SessionStore persists a chat's session runs (ADR 0026).
type SessionStore struct{ db *sql.DB }

const sessionColumns = `id, chat_id, node, visited_nodes,
	graph_context, node_context, created_at, updated_at, expires_at`

// Latest returns the most recent session for a chat or botflota.ErrNotFound.
func (s *SessionStore) Latest(ctx context.Context, chatID int64) (*botflota.Session, error) {
	query := "SELECT " + sessionColumns +
		" FROM sessions WHERE chat_id = ? ORDER BY id DESC LIMIT 1"
	var (
		session                         botflota.Session
		visitedNodes, graphCtx, nodeCtx string
		created, updated                string
		expires                         string
	)
	err := s.db.QueryRowContext(ctx, query, chatID).Scan(
		&session.ID, &session.ChatID, &session.Node, &visitedNodes, &graphCtx, &nodeCtx,
		&created, &updated, &expires,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get session: %w", err)
	}
	if err := decodeJSON(visitedNodes, &session.VisitedNodes); err != nil {
		return nil, fmt.Errorf("sqlite: visited nodes: %w", err)
	}
	if err := decodeJSON(graphCtx, &session.GraphContext); err != nil {
		return nil, fmt.Errorf("sqlite: graph context: %w", err)
	}
	if err := decodeJSON(nodeCtx, &session.NodeContext); err != nil {
		return nil, fmt.Errorf("sqlite: node context: %w", err)
	}
	session.CreatedAt = parseTime(created)
	session.UpdatedAt = parseTime(updated)
	session.ExpiresAt = parseTime(expires)
	return &session, nil
}

// Save inserts a new session run (ID zero) or updates the existing one.
func (s *SessionStore) Save(ctx context.Context, session *botflota.Session) error {
	visitedNodes, err := encodeJSON(session.VisitedNodes)
	if err != nil {
		return fmt.Errorf("sqlite: visited nodes: %w", err)
	}
	graphCtx, err := encodeJSON(session.GraphContext)
	if err != nil {
		return fmt.Errorf("sqlite: graph context: %w", err)
	}
	nodeCtx, err := encodeJSON(session.NodeContext)
	if err != nil {
		return fmt.Errorf("sqlite: node context: %w", err)
	}
	if session.ID == 0 {
		result, err := s.db.ExecContext(ctx,
			`INSERT INTO sessions (chat_id, node, visited_nodes, graph_context,
			 node_context, created_at, updated_at, expires_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			session.ChatID, session.Node, visitedNodes, graphCtx, nodeCtx,
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
		`UPDATE sessions SET node = ?, visited_nodes = ?, graph_context = ?,
		 node_context = ?, created_at = ?, updated_at = ?,
		 expires_at = ? WHERE id = ?`,
		session.Node, visitedNodes, graphCtx, nodeCtx,
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
func (s *MessageStore) Append(ctx context.Context, message *botflota.Message) error {
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
) ([]botflota.Message, error) {
	const query = `SELECT ` + messageColumns +
		` FROM messages WHERE chat_id = ? ORDER BY id LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list messages: %w", err)
	}
	defer rows.Close()
	var messages []botflota.Message
	for rows.Next() {
		var (
			message         botflota.Message
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
		message.Direction = botflota.Direction(direction)
		message.Kind = botflota.MessageKind(kind)
		message.At = parseTime(sentAt)
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

// LinkedAccountStore persists the channel identity linked to each bot
// (ADR 0006).
type LinkedAccountStore struct{ db *sql.DB }

// Get loads the linked account for (scope, bot) or returns botflota.ErrNotFound.
func (s *LinkedAccountStore) Get(
	ctx context.Context, scope, bot string,
) (*botflota.LinkedAccount, error) {
	var account botflota.LinkedAccount
	err := s.db.QueryRowContext(ctx,
		"SELECT scope, bot, jid FROM linked_accounts WHERE scope = ? AND bot = ?",
		scope, bot).
		Scan(&account.Scope, &account.Bot, &account.JID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get linked account: %w", err)
	}
	return &account, nil
}

// List returns all linked accounts in stable order.
func (s *LinkedAccountStore) List(ctx context.Context) ([]botflota.LinkedAccount, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT scope, bot, jid FROM linked_accounts ORDER BY scope, bot")
	if err != nil {
		return nil, fmt.Errorf("sqlite: list linked accounts: %w", err)
	}
	defer rows.Close()
	var accounts []botflota.LinkedAccount
	for rows.Next() {
		var account botflota.LinkedAccount
		if err := rows.Scan(&account.Scope, &account.Bot, &account.JID); err != nil {
			return nil, fmt.Errorf("sqlite: scan linked account: %w", err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list linked accounts: %w", err)
	}
	return accounts, nil
}

// Put inserts or replaces the linked account.
func (s *LinkedAccountStore) Put(ctx context.Context, account *botflota.LinkedAccount) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO linked_accounts (scope, bot, jid) VALUES (?, ?, ?)
		 ON CONFLICT (scope, bot) DO UPDATE SET jid = excluded.jid`,
		account.Scope, account.Bot, account.JID)
	if err != nil {
		return fmt.Errorf("sqlite: put linked account: %w", err)
	}
	return nil
}

// Delete removes the linked account for (scope, bot).
func (s *LinkedAccountStore) Delete(ctx context.Context, scope, bot string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM linked_accounts WHERE scope = ? AND bot = ?", scope, bot)
	if err != nil {
		return fmt.Errorf("sqlite: delete linked account: %w", err)
	}
	return nil
}

// PairingStore persists the last confirmed pairing transition of a bot
// (ADR 0011).
type PairingStore struct{ db *sql.DB }

// Get loads the last persisted pairing transition.
func (s *PairingStore) Get(
	ctx context.Context, scope, bot string,
) (*botflota.PairingStatus, error) {
	var status botflota.PairingStatus
	var changed string
	err := s.db.QueryRowContext(ctx,
		"SELECT scope, bot, state, last_change FROM pairing_status "+
			"WHERE scope = ? AND bot = ?", scope, bot).
		Scan(&status.Scope, &status.Bot, &status.State, &changed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get pairing status: %w", err)
	}
	status.LastChange = parseTime(changed)
	return &status, nil
}

// Set stores the last pairing transition for a bot.
func (s *PairingStore) Set(ctx context.Context, status *botflota.PairingStatus) error {
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

// AdminKeyStore persists the instance's single administrator key (ADR 0025).
// Only the hash is stored; the clear key lives in a 0600 file under data_dir.
type AdminKeyStore struct{ db *sql.DB }

// Get loads the stored admin key. It reports ErrNotFound until the API
// creates one on first run.
func (s *AdminKeyStore) Get(ctx context.Context) (*botflota.AdminKey, error) {
	var key botflota.AdminKey
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		"SELECT hash, created_at FROM admin_key WHERE id = 1").
		Scan(&key.Hash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get admin key: %w", err)
	}
	key.CreatedAt = parseTime(createdAt)
	return &key, nil
}

// Put replaces the stored admin key.
func (s *AdminKeyStore) Put(ctx context.Context, key *botflota.AdminKey) error {
	if key == nil || len(key.Hash) == 0 {
		return errors.New("sqlite: admin key has no hash")
	}
	if key.CreatedAt.IsZero() {
		return errors.New("sqlite: admin key has no creation time")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_key (id, hash, created_at) VALUES (1, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET hash = excluded.hash,
		 created_at = excluded.created_at`,
		key.Hash, formatTime(key.CreatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: put admin key: %w", err)
	}
	return nil
}

// APIKeyStore persists scoped API keys (ROADMAP Phase 13). Only hashes are
// stored; the clear key leaves the process once, when it is minted.
type APIKeyStore struct{ db *sql.DB }

// Create stores a new key with its scopes and sets its ID.
func (s *APIKeyStore) Create(ctx context.Context, key *botflota.APIKey) error {
	if key == nil || len(key.Hash) == 0 {
		return errors.New("sqlite: api key has no hash")
	}
	if key.Name == "" {
		return errors.New("sqlite: api key has no name")
	}
	if len(key.Scopes) == 0 {
		return errors.New("sqlite: api key has no scopes")
	}
	if key.CreatedAt.IsZero() {
		return errors.New("sqlite: api key has no creation time")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: create api key: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx,
		`INSERT INTO api_keys (name, hash, created_at, expires_at, revoked_at)
		 VALUES (?, ?, ?, ?, ?)`,
		key.Name, key.Hash, formatTime(key.CreatedAt),
		formatTime(key.ExpiresAt), formatTime(key.RevokedAt))
	if err != nil {
		return fmt.Errorf("sqlite: create api key: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: api key id: %w", err)
	}
	for _, scope := range key.Scopes {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO api_key_scopes (api_key_id, scope) VALUES (?, ?)",
			id, scope); err != nil {
			return fmt.Errorf("sqlite: create api key scope: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: create api key: %w", err)
	}
	key.ID = id
	return nil
}

// FindByHash loads a key by its hash or returns botflota.ErrNotFound. A
// revoked or expired key is still returned; the caller checks Active.
func (s *APIKeyStore) FindByHash(
	ctx context.Context, hash []byte,
) (*botflota.APIKey, error) {
	var (
		key                             botflota.APIKey
		createdAt, expiresAt, revokedAt string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, hash, created_at, expires_at, revoked_at
		 FROM api_keys WHERE hash = ?`, hash).
		Scan(&key.ID, &key.Name, &key.Hash, &createdAt, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: find api key: %w", err)
	}
	key.CreatedAt = parseTime(createdAt)
	key.ExpiresAt = parseTime(expiresAt)
	key.RevokedAt = parseTime(revokedAt)
	scopes, err := s.scopes(ctx, key.ID)
	if err != nil {
		return nil, err
	}
	key.Scopes = scopes
	return &key, nil
}

// List returns every key, oldest first, without hashes.
func (s *APIKeyStore) List(ctx context.Context) ([]botflota.APIKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at, expires_at, revoked_at
		 FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list api keys: %w", err)
	}
	defer rows.Close()
	var keys []botflota.APIKey
	for rows.Next() {
		var (
			key                             botflota.APIKey
			createdAt, expiresAt, revokedAt string
		)
		if err := rows.Scan(&key.ID, &key.Name, &createdAt, &expiresAt,
			&revokedAt); err != nil {
			return nil, err
		}
		key.CreatedAt = parseTime(createdAt)
		key.ExpiresAt = parseTime(expiresAt)
		key.RevokedAt = parseTime(revokedAt)
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlite: list api keys: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list api keys: %w", err)
	}
	for i := range keys {
		scopes, err := s.scopes(ctx, keys[i].ID)
		if err != nil {
			return nil, err
		}
		keys[i].Scopes = scopes
	}
	return keys, nil
}

// Revoke marks a key as revoked at at. Revoking an already revoked key is a
// no-op; an unknown id reports botflota.ErrNotFound.
func (s *APIKeyStore) Revoke(ctx context.Context, id int64, at time.Time) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at = ''",
		formatTime(at), id)
	if err != nil {
		return fmt.Errorf("sqlite: revoke api key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: revoke api key: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var count int
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM api_keys WHERE id = ?", id).Scan(&count)
	if err != nil {
		return fmt.Errorf("sqlite: revoke api key: %w", err)
	}
	if count == 0 {
		return botflota.ErrNotFound
	}
	return nil
}

// scopes loads a key's scopes in a stable order.
func (s *APIKeyStore) scopes(ctx context.Context, id int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT scope FROM api_key_scopes WHERE api_key_id = ? ORDER BY scope", id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: api key scopes: %w", err)
	}
	defer rows.Close()
	var scopes []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
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
