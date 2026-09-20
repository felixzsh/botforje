package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// SetDisplayName records a name learned from the channel's contact data
// (ADR 0035) without touching first or last seen. It is a no-op when the
// sender has no profile or the name is unchanged.
func (s *SenderStore) SetDisplayName(
	ctx context.Context, scope, phone, name string, at time.Time,
) error {
	if name == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE senders SET display_name = ?, name_updated_at = ?
		 WHERE scope = ? AND phone = ? AND display_name <> ?`,
		name, formatTime(at), scope, phone, name)
	if err != nil {
		return fmt.Errorf("sqlite: set sender display name: %w", err)
	}
	return nil
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
// sender profile and the stored message counts by ingest kind. An empty bot
// means every bot in the scope.
func (s *ChatStore) Recent(
	ctx context.Context, scope, bot string, limit int,
) ([]botflota.ChatSummary, error) {
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

// NoteHistory widens a chat's activity to cover imported history without
// touching the live inbound count (ADR 0035). Comparisons are on the stored
// RFC 3339 text, which orders correctly across the day boundaries that matter
// here.
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

// CountActive returns how many session runs of one bot are unexpired at at. A
// run without an expiry never expires (botflota.Session.Expired).
func (s *SessionStore) CountActive(
	ctx context.Context, scope, bot string, at time.Time,
) (int, error) {
	const query = `SELECT COUNT(*) FROM sessions
		JOIN chats ON chats.id = sessions.chat_id
		WHERE chats.scope = ? AND chats.bot = ?
		AND (sessions.expires_at = '' OR sessions.expires_at > ?)`
	var count int
	err := s.db.QueryRowContext(ctx, query, scope, bot, formatTime(at)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count active sessions: %w", err)
	}
	return count, nil
}

// MessageStore persists message history (ADR 0026).
type MessageStore struct{ db *sql.DB }

const messageColumns = `id, chat_id, session_id, direction, kind, body, sent_at,
	provider_message_id, ingest_kind, origin, routing`

// Append inserts the message and sets its ID. A message whose provider id is
// already present in its chat is a duplicate and reports botflota.ErrDuplicate
// instead of inserting (ADR 0026).
func (s *MessageStore) Append(ctx context.Context, message *botflota.Message) error {
	var sessionID any
	if message.SessionID != 0 {
		sessionID = message.SessionID
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (chat_id, session_id, direction, kind, body, sent_at,
		 provider_message_id, ingest_kind, origin, routing)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT DO NOTHING`,
		message.ChatID, sessionID, string(message.Direction), string(message.Kind),
		message.Body, formatTime(message.At), message.ProviderMessageID,
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

// SetProviderID fills the channel id of an already stored outbound message
// once the channel reports it, so a later history sync can deduplicate it. An
// empty id is ignored.
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

// Oldest returns the oldest stored message of a chat that carries a channel
// id, the anchor an on-demand history request pages back from; a chat with no
// such message reports botflota.ErrNotFound (ADR 0035).
func (s *MessageStore) Oldest(ctx context.Context, chatID int64) (*botflota.Message, error) {
	const query = `SELECT ` + messageColumns +
		` FROM messages WHERE chat_id = ? AND provider_message_id != ''
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

// History returns up to limit messages of a chat, oldest first: the last ones
// when afterID is zero, or the messages newer than afterID otherwise.
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
			message                 botflota.Message
			sessionID               sql.NullInt64
			direction, kind, ingest string
			origin, routing         string
			sentAt                  string
		)
		if err := rows.Scan(&message.ID, &message.ChatID, &sessionID,
			&direction, &kind, &message.Body, &sentAt,
			&message.ProviderMessageID, &ingest, &origin, &routing); err != nil {
			return nil, err
		}
		if sessionID.Valid {
			message.SessionID = sessionID.Int64
		}
		message.Direction = botflota.Direction(direction)
		message.Kind = botflota.MessageKind(kind)
		message.Ingest = botflota.Ingest(ingest)
		message.Origin = botflota.MessageOrigin(origin)
		message.Routing = botflota.RoutingMethod(routing)
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

// OwnerTokenStore persists the instance's single owner token (ADR 0025). Only
// the hash is stored; the clear token lives in a 0600 file under data_dir.
type OwnerTokenStore struct{ db *sql.DB }

// Get loads the stored owner token. It reports ErrNotFound until the API
// creates one on first run.
func (s *OwnerTokenStore) Get(ctx context.Context) (*botflota.OwnerToken, error) {
	var token botflota.OwnerToken
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		"SELECT hash, created_at FROM owner_token WHERE id = 1").
		Scan(&token.Hash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get owner token: %w", err)
	}
	token.CreatedAt = parseTime(createdAt)
	return &token, nil
}

// Put replaces the stored owner token.
func (s *OwnerTokenStore) Put(ctx context.Context, token *botflota.OwnerToken) error {
	if token == nil || len(token.Hash) == 0 {
		return errors.New("sqlite: owner token has no hash")
	}
	if token.CreatedAt.IsZero() {
		return errors.New("sqlite: owner token has no creation time")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO owner_token (id, hash, created_at) VALUES (1, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET hash = excluded.hash,
		 created_at = excluded.created_at`,
		token.Hash, formatTime(token.CreatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: put owner token: %w", err)
	}
	return nil
}

// AccessTokenStore persists scoped access tokens (ROADMAP Phase 13). Only
// hashes are stored; the clear token leaves the process once, when it is
// minted.
type AccessTokenStore struct{ db *sql.DB }

// Create stores a new token with its scopes and sets its ID.
func (s *AccessTokenStore) Create(ctx context.Context, token *botflota.AccessToken) error {
	if token == nil || len(token.Hash) == 0 {
		return errors.New("sqlite: access token has no hash")
	}
	if token.Name == "" {
		return errors.New("sqlite: access token has no name")
	}
	if len(token.Scopes) == 0 {
		return errors.New("sqlite: access token has no scopes")
	}
	if token.CreatedAt.IsZero() {
		return errors.New("sqlite: access token has no creation time")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: create access token: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx,
		`INSERT INTO access_tokens (name, hash, created_at, expires_at, revoked_at)
		 VALUES (?, ?, ?, ?, ?)`,
		token.Name, token.Hash, formatTime(token.CreatedAt),
		formatTime(token.ExpiresAt), formatTime(token.RevokedAt))
	if err != nil {
		return fmt.Errorf("sqlite: create access token: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: access token id: %w", err)
	}
	for _, scope := range token.Scopes {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO access_token_scopes (access_token_id, scope) VALUES (?, ?)",
			id, scope); err != nil {
			return fmt.Errorf("sqlite: create access token scope: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: create access token: %w", err)
	}
	token.ID = id
	return nil
}

// FindByHash loads a token by its hash or returns botflota.ErrNotFound. A
// revoked or expired token is still returned; the caller checks Active.
func (s *AccessTokenStore) FindByHash(
	ctx context.Context, hash []byte,
) (*botflota.AccessToken, error) {
	var (
		token                           botflota.AccessToken
		createdAt, expiresAt, revokedAt string
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, hash, created_at, expires_at, revoked_at
		 FROM access_tokens WHERE hash = ?`, hash).
		Scan(&token.ID, &token.Name, &token.Hash, &createdAt, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: find access token: %w", err)
	}
	token.CreatedAt = parseTime(createdAt)
	token.ExpiresAt = parseTime(expiresAt)
	token.RevokedAt = parseTime(revokedAt)
	scopes, err := s.scopes(ctx, token.ID)
	if err != nil {
		return nil, err
	}
	token.Scopes = scopes
	return &token, nil
}

// List returns every token, oldest first, without hashes.
func (s *AccessTokenStore) List(ctx context.Context) ([]botflota.AccessToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at, expires_at, revoked_at
		 FROM access_tokens ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list access tokens: %w", err)
	}
	defer rows.Close()
	var tokens []botflota.AccessToken
	for rows.Next() {
		var (
			token                           botflota.AccessToken
			createdAt, expiresAt, revokedAt string
		)
		if err := rows.Scan(&token.ID, &token.Name, &createdAt, &expiresAt,
			&revokedAt); err != nil {
			return nil, err
		}
		token.CreatedAt = parseTime(createdAt)
		token.ExpiresAt = parseTime(expiresAt)
		token.RevokedAt = parseTime(revokedAt)
		tokens = append(tokens, token)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlite: list access tokens: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list access tokens: %w", err)
	}
	for i := range tokens {
		scopes, err := s.scopes(ctx, tokens[i].ID)
		if err != nil {
			return nil, err
		}
		tokens[i].Scopes = scopes
	}
	return tokens, nil
}

// Revoke marks a token as revoked at at. Revoking an already revoked token is
// a no-op; an unknown id reports botflota.ErrNotFound.
func (s *AccessTokenStore) Revoke(ctx context.Context, id int64, at time.Time) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE access_tokens SET revoked_at = ? WHERE id = ? AND revoked_at = ''",
		formatTime(at), id)
	if err != nil {
		return fmt.Errorf("sqlite: revoke access token: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: revoke access token: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var count int
	err = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM access_tokens WHERE id = ?", id).Scan(&count)
	if err != nil {
		return fmt.Errorf("sqlite: revoke access token: %w", err)
	}
	if count == 0 {
		return botflota.ErrNotFound
	}
	return nil
}

// scopes loads a token's scopes in a stable order.
func (s *AccessTokenStore) scopes(ctx context.Context, id int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT scope FROM access_token_scopes WHERE access_token_id = ? ORDER BY scope",
		id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: access token scopes: %w", err)
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
