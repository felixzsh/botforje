package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/felixzsh/botflota"
)

type OwnerTokenStore struct{ db *sql.DB }

func NewOwnerTokenStore(db *sql.DB) *OwnerTokenStore {
	return &OwnerTokenStore{db: db}
}

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
