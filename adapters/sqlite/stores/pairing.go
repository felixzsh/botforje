package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/felixzsh/botflota"
)

type PairingStore struct{ db *sql.DB }

func NewPairingStore(db *sql.DB) *PairingStore { return &PairingStore{db: db} }

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
