package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/felixzsh/botflota"
)

type BindingStore struct{ db *sql.DB }

func NewBindingStore(db *sql.DB) *BindingStore {
	return &BindingStore{db: db}
}

func (s *BindingStore) Get(
	ctx context.Context, scope, bot string,
) (*botflota.Binding, error) {
	var account botflota.Binding
	err := s.db.QueryRowContext(ctx,
		`SELECT scope, bot, provider, subject FROM bindings
		 WHERE scope = ? AND bot = ?`, scope, bot).
		Scan(&account.Scope, &account.Bot,
			&account.Identity.Provider, &account.Identity.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get linked account: %w", err)
	}
	return &account, nil
}

func (s *BindingStore) List(ctx context.Context) ([]botflota.Binding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT scope, bot, provider, subject FROM bindings ORDER BY scope, bot`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list linked accounts: %w", err)
	}
	defer rows.Close()
	var accounts []botflota.Binding
	for rows.Next() {
		var account botflota.Binding
		if err := rows.Scan(&account.Scope, &account.Bot,
			&account.Identity.Provider, &account.Identity.Subject); err != nil {
			return nil, fmt.Errorf("sqlite: scan linked account: %w", err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list linked accounts: %w", err)
	}
	return accounts, nil
}

func (s *BindingStore) Put(ctx context.Context, account *botflota.Binding) error {
	if !account.Identity.Valid() {
		return errors.New("sqlite: linked account needs provider and subject")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO bindings (scope, bot, provider, subject)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (scope, bot) DO UPDATE SET
		   provider = excluded.provider, subject = excluded.subject`,
		account.Scope, account.Bot, account.Identity.Provider, account.Identity.Subject)
	if err != nil {
		return fmt.Errorf("sqlite: put linked account: %w", err)
	}
	return nil
}

func (s *BindingStore) Delete(ctx context.Context, scope, bot string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM bindings WHERE scope = ? AND bot = ?", scope, bot)
	if err != nil {
		return fmt.Errorf("sqlite: delete linked account: %w", err)
	}
	return nil
}
