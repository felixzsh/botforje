package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/felixzsh/botflota"
)

type LinkedAccountStore struct{ db *sql.DB }

func NewLinkedAccountStore(db *sql.DB) *LinkedAccountStore {
	return &LinkedAccountStore{db: db}
}

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

func (s *LinkedAccountStore) Delete(ctx context.Context, scope, bot string) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM linked_accounts WHERE scope = ? AND bot = ?", scope, bot)
	if err != nil {
		return fmt.Errorf("sqlite: delete linked account: %w", err)
	}
	return nil
}
