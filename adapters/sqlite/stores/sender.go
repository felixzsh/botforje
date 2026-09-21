package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

type SenderStore struct{ db *sql.DB }

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
