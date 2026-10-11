package stores

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/felixzsh/botflota"
)

// PeerStore records one person's durable identity per scope (ADR 0029,
// ADR 0051).
type PeerStore struct{ db dbtx }

func NewPeerStore(db *sql.DB) *PeerStore { return &PeerStore{db: db} }

// peerReadColumns are the stored identity columns plus derived reader flags.
const peerReadColumns = `p.id, p.scope, p.provider, p.subject, p.display_name,
	p.name_updated_at, p.first_seen, p.last_seen,
	EXISTS (SELECT 1 FROM directory_entries d
		WHERE d.scope = p.scope AND d.provider = p.provider
		AND d.subject = p.subject),
	EXISTS (SELECT 1 FROM chats ch
		WHERE ch.scope = p.scope AND ch.peer_id = p.id),
	EXISTS (SELECT 1 FROM chats ch JOIN messages m ON m.chat_id = ch.id
		WHERE ch.scope = p.scope AND ch.peer_id = p.id
		AND m.ingest_kind = 'sync')`

// EnsurePeer creates an identity without a first inbound message, so the
// directory, an imported history row, and an API-origin send never consume the
// first-contact hook. An existing identity is returned unchanged.
func (s *PeerStore) EnsurePeer(
	ctx context.Context, scope string, key botflota.IdentityKey, at time.Time,
) (*botflota.Peer, error) {
	if !key.Valid() {
		return nil, errors.New("sqlite: peer identity needs provider and subject")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO peers (scope, provider, subject) VALUES (?, ?, ?)
		 ON CONFLICT (scope, provider, subject) DO NOTHING`,
		scope, key.Provider, key.Subject)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ensure peer: %w", err)
	}
	return s.get(ctx, s.db, scope, key)
}

// RecordInbound records a live inbound message on an identity, creating it
// when missing, and reports whether this was the person's first inbound in the
// scope: the first-contact hook (ADR 0029, ADR 0045). The claim of first_seen
// is a conditional update whose affected rows are the answer, so two messages
// that share a timestamp still report first only once. The claim, the
// activity, and the name land in one transaction — its own when this store
// holds a connection, the caller's turn when it was composed into one — so a
// failure rolls the claim back and the next message reports first again.
func (s *PeerStore) RecordInbound(
	ctx context.Context, scope string, key botflota.IdentityKey, name string,
	at time.Time,
) (*botflota.Peer, bool, error) {
	if !key.Valid() {
		return nil, false, errors.New("sqlite: peer identity needs provider and subject")
	}
	if db, ok := s.db.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, false, fmt.Errorf("sqlite: begin peer message: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		peer, first, err := s.recordInbound(ctx, tx, scope, key, name, at)
		if err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("sqlite: commit peer message: %w", err)
		}
		return peer, first, nil
	}
	return s.recordInbound(ctx, s.db, scope, key, name, at)
}

func (s *PeerStore) recordInbound(
	ctx context.Context, q dbtx, scope string, key botflota.IdentityKey,
	name string, at time.Time,
) (*botflota.Peer, bool, error) {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO peers (scope, provider, subject) VALUES (?, ?, ?)
		 ON CONFLICT (scope, provider, subject) DO NOTHING`,
		scope, key.Provider, key.Subject); err != nil {
		return nil, false, fmt.Errorf("sqlite: ensure peer for inbound: %w", err)
	}
	// The claim is the answer: a row updated here was the first empty
	// first_seen this peer had, whatever its timestamp.
	result, err := q.ExecContext(ctx,
		`UPDATE peers SET first_seen = ?
		 WHERE scope = ? AND provider = ? AND subject = ? AND first_seen = ''`,
		formatTime(at), scope, key.Provider, key.Subject)
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: claim first peer: %w", err)
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: claim first peer: %w", err)
	}
	nameAt := ""
	if name != "" {
		nameAt = formatTime(at)
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE peers SET
		   last_seen = ?,
		   display_name = CASE WHEN ? <> '' THEN ? ELSE display_name END,
		   name_updated_at = CASE
		     WHEN ? <> '' AND ? <> display_name THEN ? ELSE name_updated_at END
		 WHERE scope = ? AND provider = ? AND subject = ?`,
		formatTime(at), name, name, name, name, nameAt,
		scope, key.Provider, key.Subject); err != nil {
		return nil, false, fmt.Errorf("sqlite: update peer message: %w", err)
	}
	peer, err := s.get(ctx, q, scope, key)
	if err != nil {
		return nil, false, err
	}
	return peer, claimed == 1, nil
}

func (s *PeerStore) SetDisplayName(
	ctx context.Context, scope string, key botflota.IdentityKey, name string,
	at time.Time,
) error {
	if name == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE peers SET display_name = ?, name_updated_at = ?
		 WHERE scope = ? AND provider = ? AND subject = ? AND display_name = ''`,
		name, formatTime(at), scope, key.Provider, key.Subject)
	if err != nil {
		return fmt.Errorf("sqlite: set peer display name: %w", err)
	}
	return nil
}

func (s *PeerStore) Get(
	ctx context.Context, scope string, key botflota.IdentityKey,
) (*botflota.Peer, error) {
	return s.get(ctx, s.db, scope, key)
}

func (s *PeerStore) get(
	ctx context.Context, q dbtx, scope string, key botflota.IdentityKey,
) (*botflota.Peer, error) {
	row := q.QueryRowContext(ctx,
		"SELECT "+peerReadColumns+
			" FROM peers p WHERE p.scope = ? AND p.provider = ? AND p.subject = ?",
		scope, key.Provider, key.Subject)
	peer, err := scanPeerRead(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get peer: %w", err)
	}
	return peer, nil
}

// List returns up to limit identities of one scope, most recently seen first.
// The keyset cursor is the previous page's last row, so a page never skips a
// row; activity moving between pages can still surface a person twice, which
// the read API accepts for a directory listing (ADR 0044).
func (s *PeerStore) List(
	ctx context.Context, scope string, since time.Time, limit int, cursor string,
) (botflota.PeerPage, error) {
	if limit <= 0 {
		return botflota.PeerPage{}, nil
	}
	query := "SELECT " + peerReadColumns + " FROM peers p WHERE p.scope = ?"
	args := []any{scope}
	if !since.IsZero() {
		query += " AND p.first_seen >= ?"
		args = append(args, formatTime(since))
	}
	if cursor != "" {
		lastSeen, id, err := decodePeerCursor(cursor)
		if err != nil {
			return botflota.PeerPage{}, err
		}
		query += " AND (p.last_seen < ? OR (p.last_seen = ? AND p.id < ?))"
		args = append(args, formatTime(lastSeen), formatTime(lastSeen), id)
	}
	// One extra row tells the caller whether another page follows.
	query += " ORDER BY p.last_seen DESC, p.id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return botflota.PeerPage{}, fmt.Errorf("sqlite: list peers: %w", err)
	}
	defer rows.Close()
	var peers []botflota.Peer
	for rows.Next() {
		peer, err := scanPeerRead(rows)
		if err != nil {
			return botflota.PeerPage{}, err
		}
		peers = append(peers, *peer)
	}
	if err := rows.Err(); err != nil {
		return botflota.PeerPage{}, err
	}
	page := botflota.PeerPage{Peers: peers}
	if len(peers) > limit {
		page.Peers = peers[:limit]
		last := page.Peers[limit-1]
		page.NextCursor = encodePeerCursor(last.LastSeen, last.ID)
	}
	return page, nil
}

func scanPeerRead(row interface{ Scan(...any) error }) (*botflota.Peer, error) {
	var (
		peer                        botflota.Peer
		nameAt, firstSeen, lastSeen string
	)
	if err := row.Scan(&peer.ID, &peer.Scope, &peer.Key.Provider, &peer.Key.Subject,
		&peer.DisplayName, &nameAt, &firstSeen, &lastSeen,
		&peer.AddressBookImported, &peer.HasChat, &peer.HasHistory); err != nil {
		return nil, err
	}
	peer.NameUpdatedAt = parseTime(nameAt)
	peer.FirstSeen = parseTime(firstSeen)
	peer.LastSeen = parseTime(lastSeen)
	return &peer, nil
}

// The cursor is opaque to callers: the last row's activity and id, encoded so
// a page cannot be hand-built from a row a caller can see (ADR 0044).
func encodePeerCursor(lastSeen time.Time, id int64) string {
	raw := formatTime(lastSeen) + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodePeerCursor(cursor string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	at := strings.LastIndexByte(string(raw), '|')
	if at < 0 {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	lastSeen := parseTime(string(raw[:at]))
	id, err := strconv.ParseInt(string(raw[at+1:]), 10, 64)
	if err != nil {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	return lastSeen, id, nil
}
