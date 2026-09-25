package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/felixzsh/botflota"
)

type SessionStore struct{ db dbtx }

func NewSessionStore(db *sql.DB) *SessionStore { return &SessionStore{db: db} }

const sessionColumns = `id, chat_id, node, visited_nodes,
	graph_context, node_context, created_at, updated_at, expires_at`

func (s *SessionStore) Latest(ctx context.Context, chatID int64) (*botflota.Session, error) {
	query := "SELECT " + sessionColumns +
		" FROM sessions WHERE chat_id = ? ORDER BY id DESC LIMIT 1"
	session, err := scanSession(s.db.QueryRowContext(ctx, query, chatID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get session: %w", err)
	}
	return &session, nil
}

// rowScanner is what *sql.Row and *sql.Rows share, so one session is decoded the
// same way whoever read it: alone, or streamed with the rest of a chat's runs.
type rowScanner interface{ Scan(dest ...any) error }

func scanSession(row rowScanner) (botflota.Session, error) {
	var (
		session                         botflota.Session
		visitedNodes, graphCtx, nodeCtx string
		created, updated                string
		expires                         string
	)
	err := row.Scan(
		&session.ID, &session.ChatID, &session.Node, &visitedNodes, &graphCtx, &nodeCtx,
		&created, &updated, &expires,
	)
	if err != nil {
		return botflota.Session{}, err
	}
	if err := decodeJSON(visitedNodes, &session.VisitedNodes); err != nil {
		return botflota.Session{}, fmt.Errorf("sqlite: visited nodes: %w", err)
	}
	if err := decodeJSON(graphCtx, &session.GraphContext); err != nil {
		return botflota.Session{}, fmt.Errorf("sqlite: graph context: %w", err)
	}
	if err := decodeJSON(nodeCtx, &session.NodeContext); err != nil {
		return botflota.Session{}, fmt.Errorf("sqlite: node context: %w", err)
	}
	session.CreatedAt = parseTime(created)
	session.UpdatedAt = parseTime(updated)
	session.ExpiresAt = parseTime(expires)
	return session, nil
}

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

func (s *SessionStore) Delete(ctx context.Context, chatID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE chat_id = ?", chatID)
	if err != nil {
		return fmt.Errorf("sqlite: delete session: %w", err)
	}
	return nil
}

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

func (s *SessionStore) HasActiveSessionOutsideNodes(
	ctx context.Context, scope, bot string, nodes []string, at time.Time,
) (bool, error) {
	query := `SELECT EXISTS (
		SELECT 1 FROM sessions
		JOIN chats ON chats.id = sessions.chat_id
		WHERE chats.scope = ? AND chats.bot = ?
		AND (sessions.expires_at = '' OR sessions.expires_at > ?)`
	args := []any{scope, bot, formatTime(at)}
	if len(nodes) > 0 {
		query += " AND sessions.node NOT IN (" + strings.TrimSuffix(
			strings.Repeat("?,", len(nodes)), ",") + ")"
		for _, node := range nodes {
			args = append(args, node)
		}
	}
	query += ")"
	var found bool
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&found); err != nil {
		return false, fmt.Errorf("sqlite: inspect active session nodes: %w", err)
	}
	return found, nil
}
