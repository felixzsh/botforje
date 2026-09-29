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

type SenderStore struct{ db dbtx }

func NewSenderStore(db *sql.DB) *SenderStore { return &SenderStore{db: db} }

const senderColumns = `id, scope, phone, display_name, name_updated_at, first_seen, last_seen`

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

func (s *SenderStore) Get(
	ctx context.Context, scope, phone string,
) (*botflota.Sender, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+senderColumns+" FROM senders WHERE scope = ? AND phone = ?",
		scope, phone)
	sender, err := scanSender(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get sender: %w", err)
	}
	return sender, nil
}

// List returns up to limit profiles of one scope, most recently seen first.
// The keyset cursor is the previous page's last row, so a page never skips a
// row; activity moving between pages can still surface a person twice, which
// the read API accepts for a phone-book listing (ADR 0044).
func (s *SenderStore) List(
	ctx context.Context, scope string, since time.Time, limit int, cursor string,
) (botflota.SenderPage, error) {
	if limit <= 0 {
		return botflota.SenderPage{}, nil
	}
	query := "SELECT " + senderColumns + " FROM senders WHERE scope = ?"
	args := []any{scope}
	if !since.IsZero() {
		query += " AND first_seen >= ?"
		args = append(args, formatTime(since))
	}
	if cursor != "" {
		lastSeen, id, err := decodeSenderCursor(cursor)
		if err != nil {
			return botflota.SenderPage{}, err
		}
		query += " AND (last_seen < ? OR (last_seen = ? AND id < ?))"
		args = append(args, formatTime(lastSeen), formatTime(lastSeen), id)
	}
	// One extra row tells the caller whether another page follows.
	query += " ORDER BY last_seen DESC, id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return botflota.SenderPage{}, fmt.Errorf("sqlite: list senders: %w", err)
	}
	defer rows.Close()
	var senders []botflota.Sender
	for rows.Next() {
		sender, err := scanSender(rows)
		if err != nil {
			return botflota.SenderPage{}, err
		}
		senders = append(senders, *sender)
	}
	if err := rows.Err(); err != nil {
		return botflota.SenderPage{}, err
	}
	page := botflota.SenderPage{Senders: senders}
	if len(senders) > limit {
		page.Senders = senders[:limit]
		last := page.Senders[limit-1]
		page.NextCursor = encodeSenderCursor(last.LastSeen, last.ID)
	}
	return page, nil
}

func scanSender(row interface{ Scan(...any) error }) (*botflota.Sender, error) {
	var (
		sender                      botflota.Sender
		nameAt, firstSeen, lastSeen string
	)
	if err := row.Scan(&sender.ID, &sender.Scope, &sender.Phone,
		&sender.DisplayName, &nameAt, &firstSeen, &lastSeen); err != nil {
		return nil, err
	}
	sender.NameUpdatedAt = parseTime(nameAt)
	sender.FirstSeen = parseTime(firstSeen)
	sender.LastSeen = parseTime(lastSeen)
	return &sender, nil
}

// The cursor is opaque to callers: the last row's activity and id, encoded so
// a page cannot be hand-built from a row a caller can see (ADR 0044).
func encodeSenderCursor(lastSeen time.Time, id int64) string {
	raw := formatTime(lastSeen) + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeSenderCursor(cursor string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	at := strings.LastIndexByte(string(raw), '|')
	if at < 0 {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	lastSeen, err := time.Parse(time.RFC3339Nano, string(raw[:at]))
	if err != nil {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	id, err := strconv.ParseInt(string(raw[at+1:]), 10, 64)
	if err != nil || id < 0 {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	return lastSeen, id, nil
}
