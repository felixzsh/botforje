package stores

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/felixzsh/botflota"
)

// HistoryStore reads one bot's history in a single snapshot (ADR 0042). A read
// transaction keeps an arrival mid-read from appearing halfway through, so an
// exported conversation is one moment in time.
type HistoryStore struct{ db *sql.DB }

func NewHistoryStore(db *sql.DB) *HistoryStore { return &HistoryStore{db: db} }

func (s *HistoryStore) ReadHistory(
	ctx context.Context, scope, bot string, sink botflota.HistorySink,
) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("sqlite: begin history read: %w", err)
	}
	// Read-only: the rollback only ends the snapshot.
	defer func() { _ = tx.Rollback() }()
	for _, table := range []struct {
		name botflota.HistoryTable
		read func(context.Context, *sql.Tx, string, string, botflota.HistorySink) error
	}{
		{botflota.TableChats, readHistoryChats},
		{botflota.TableSessions, readHistorySessions},
		{botflota.TableMessages, readHistoryMessages},
		{botflota.TableDirectory, readHistoryDirectory},
	} {
		if err := sink.BeginTable(table.name); err != nil {
			return err
		}
		if err := table.read(ctx, tx, scope, bot, sink); err != nil {
			return err
		}
	}
	return nil
}

// readHistoryChats carries the peer's key and name, which the scope owns and
// keeps, so the exported file can be read without them (ADR 0029).
func readHistoryChats(
	ctx context.Context, tx *sql.Tx, scope, bot string, sink botflota.HistorySink,
) error {
	// Identities are joined by subquery rather than a join, so the shared column
	// list stays unqualified and cannot drift from the chat store's.
	rows, err := tx.QueryContext(ctx,
		`SELECT `+chatColumns+`,
			(SELECT provider FROM peers WHERE peers.id = chats.peer_id),
			(SELECT subject FROM peers WHERE peers.id = chats.peer_id),
			(SELECT display_name FROM peers WHERE peers.id = chats.peer_id)
		 FROM chats WHERE chats.scope = ? AND chats.bot = ? ORDER BY chats.id`,
		scope, bot)
	if err != nil {
		return fmt.Errorf("sqlite: read history chats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		chat, err := scanHistoryChat(rows)
		if err != nil {
			return fmt.Errorf("sqlite: read history chats: %w", err)
		}
		if err := sink.Chat(chat); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: read history chats: %w", err)
	}
	return nil
}

func scanHistoryChat(rows *sql.Rows) (botflota.HistoryChat, error) {
	var (
		chat                 botflota.HistoryChat
		kind                 string
		peerID               sql.NullInt64
		startedAt, updatedAt string
		guards               string
		peerProvider         sql.NullString
		peerSubject          sql.NullString
		peerName             sql.NullString
	)
	err := rows.Scan(&chat.ID, &chat.Scope, &chat.Bot, &chat.Target.Provider,
		&kind, &chat.Target.Thread, &chat.Target.Topic, &peerID,
		&startedAt, &updatedAt, &chat.MessageCount, &guards,
		&peerProvider, &peerSubject, &peerName)
	if err != nil {
		return botflota.HistoryChat{}, err
	}
	chat.Target.Kind = botflota.ChatKind(kind)
	if peerID.Valid {
		chat.PeerID = peerID.Int64
	}
	chat.PeerKey = botflota.IdentityKey{
		Provider: peerProvider.String, Subject: peerSubject.String,
	}
	chat.PeerName = peerName.String
	chat.StartedAt, chat.UpdatedAt = parseTime(startedAt), parseTime(updatedAt)
	if err := decodeJSON(guards, &chat.Guards); err != nil {
		return botflota.HistoryChat{}, fmt.Errorf("sqlite: decode guards: %w", err)
	}
	return chat, nil
}

func readHistorySessions(
	ctx context.Context, tx *sql.Tx, scope, bot string, sink botflota.HistorySink,
) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions
		 WHERE chat_id IN (SELECT id FROM chats WHERE scope = ? AND bot = ?)
		 ORDER BY id`, scope, bot)
	if err != nil {
		return fmt.Errorf("sqlite: read history sessions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return fmt.Errorf("sqlite: read history sessions: %w", err)
		}
		if err := sink.Session(session); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: read history sessions: %w", err)
	}
	return nil
}

func readHistoryMessages(
	ctx context.Context, tx *sql.Tx, scope, bot string, sink botflota.HistorySink,
) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages
		 WHERE chat_id IN (SELECT id FROM chats WHERE scope = ? AND bot = ?)
		 ORDER BY id`, scope, bot)
	if err != nil {
		return fmt.Errorf("sqlite: read history messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return fmt.Errorf("sqlite: read history messages: %w", err)
		}
		if err := sink.Message(message); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: read history messages: %w", err)
	}
	return nil
}

func readHistoryDirectory(
	ctx context.Context, tx *sql.Tx, scope, bot string, sink botflota.HistorySink,
) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT `+directoryColumns+` FROM directory_entries
		 WHERE scope = ? AND bot = ?
		 ORDER BY provider, subject, source_provider, source_subject`, scope, bot)
	if err != nil {
		return fmt.Errorf("sqlite: read history directory: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		entry, err := scanHistoryDirectory(rows)
		if err != nil {
			return fmt.Errorf("sqlite: read history directory: %w", err)
		}
		if err := sink.DirectoryEntry(entry); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: read history directory: %w", err)
	}
	return nil
}

func scanHistoryDirectory(rows *sql.Rows) (botflota.DirectoryEntry, error) {
	var (
		entry     botflota.DirectoryEntry
		updatedAt string
	)
	err := rows.Scan(&entry.Scope, &entry.Bot,
		&entry.PeerKey.Provider, &entry.PeerKey.Subject,
		&entry.SourceIdentity.Provider, &entry.SourceIdentity.Subject,
		&entry.DisplayName, &updatedAt)
	if err != nil {
		return botflota.DirectoryEntry{}, err
	}
	entry.UpdatedAt = parseTime(updatedAt)
	return entry, nil
}
