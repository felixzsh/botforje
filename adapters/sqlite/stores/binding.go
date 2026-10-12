package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/felixzsh/botflota"
)

type BindingStore struct{ db *sql.DB }

func NewBindingStore(db *sql.DB) *BindingStore {
	return &BindingStore{db: db}
}

func scanBinding(row interface {
	Scan(dest ...any) error
}) (*botflota.Binding, error) {
	var account botflota.Binding
	var created, updated string
	err := row.Scan(
		&account.Scope, &account.Bot,
		&account.Provider, &account.Adapter, &account.Mode, &account.Namespace,
		&account.Identity.Subject,
		&account.Generation, &created, &updated,
	)
	if err != nil {
		return nil, err
	}
	account.Identity.Provider = account.Provider
	if account.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, fmt.Errorf("sqlite: parse binding creation: %w", err)
	}
	if account.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return nil, fmt.Errorf("sqlite: parse binding update: %w", err)
	}
	return &account, nil
}

const bindingColumns = `scope, bot, provider, adapter, mode, namespace,
		subject, generation, created_at, updated_at`

func (s *BindingStore) Get(
	ctx context.Context, scope, bot string,
) (*botflota.Binding, error) {
	account, err := scanBinding(s.db.QueryRowContext(ctx,
		`SELECT `+bindingColumns+` FROM bindings WHERE scope = ? AND bot = ?`,
		scope, bot))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get linked account: %w", err)
	}
	return account, nil
}

func (s *BindingStore) List(ctx context.Context) ([]botflota.Binding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+bindingColumns+` FROM bindings ORDER BY scope, bot`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list linked accounts: %w", err)
	}
	defer rows.Close()
	var accounts []botflota.Binding
	for rows.Next() {
		account, err := scanBinding(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan linked account: %w", err)
		}
		accounts = append(accounts, *account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list linked accounts: %w", err)
	}
	return accounts, nil
}

// Commit records one bot's committed account association: the neutral
// definition metadata (provider, adapter kind, account mode), the stable
// identity-space context, and the normalized own identity (ADR 0051,
// ADR 0053). The first commit of a bot starts generation one; renewing the
// same identity space keeps it, and an unrelated replacement advances it
// past the stored one. The SDK device locator and the credentials stay in
// adapter-owned storage; this row never carries them.
func (s *BindingStore) Commit(ctx context.Context, account *botflota.Binding) error {
	if account.Scope == "" || account.Bot == "" {
		return errors.New("sqlite: linked account needs scope and bot")
	}
	if account.Provider == "" {
		return errors.New("sqlite: linked account needs a provider")
	}
	if !account.Identity.Valid() {
		return errors.New("sqlite: linked account needs provider and subject")
	}
	if account.Identity.Provider != account.Provider {
		return errors.New("sqlite: linked account identity names another provider")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: commit linked account: %w", err)
	}
	defer tx.Rollback()
	var stored struct {
		provider   string
		namespace  string
		subject    string
		generation int64
		created    string
	}
	err = tx.QueryRowContext(ctx,
		`SELECT provider, namespace, subject, generation, created_at
		 FROM bindings WHERE scope = ? AND bot = ?`,
		account.Scope, account.Bot).Scan(
		&stored.provider, &stored.namespace, &stored.subject,
		&stored.generation, &stored.created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		account.Generation = 1
		account.CreatedAt, _ = time.Parse(time.RFC3339Nano, now)
	case err != nil:
		return fmt.Errorf("sqlite: read linked account: %w", err)
	case stored.provider == account.Provider &&
		stored.namespace == account.Namespace &&
		stored.subject == account.Identity.Subject:
		account.Generation = stored.generation
		account.CreatedAt, _ = time.Parse(time.RFC3339Nano, stored.created)
	default:
		account.Generation = stored.generation + 1
		account.CreatedAt, _ = time.Parse(time.RFC3339Nano, stored.created)
	}
	account.UpdatedAt, _ = time.Parse(time.RFC3339Nano, now)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO bindings (scope, bot, provider, adapter, mode, namespace,
			subject, generation, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (scope, bot) DO UPDATE SET
		   provider = excluded.provider, adapter = excluded.adapter,
		   mode = excluded.mode, namespace = excluded.namespace,
		   subject = excluded.subject, generation = excluded.generation,
		   updated_at = excluded.updated_at`,
		account.Scope, account.Bot, account.Provider, account.Adapter,
		account.Mode, account.Namespace, account.Identity.Subject,
		account.Generation, account.CreatedAt.Format(time.RFC3339Nano), now); err != nil {
		return fmt.Errorf("sqlite: commit linked account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit linked account: %w", err)
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
