package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

type AccessTokenStore struct{ db *sql.DB }

func NewAccessTokenStore(db *sql.DB) *AccessTokenStore {
	return &AccessTokenStore{db: db}
}

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
