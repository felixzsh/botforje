package stores

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/felixzsh/botflota"
)

// AddressBookStore records the imported address-book entries of one bot's
// linked account, keyed by (scope, bot, phone) (ADR 0037).
type AddressBookStore struct{ db *sql.DB }

func NewAddressBookStore(db *sql.DB) *AddressBookStore {
	return &AddressBookStore{db: db}
}

const addressBookColumns = `scope, bot, phone, name, push_name, business_name, updated_at`

func (s *AddressBookStore) Put(
	ctx context.Context, entry *botflota.AddressBookEntry,
) error {
	if entry == nil || entry.Scope == "" || entry.Bot == "" || entry.Phone == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO bot_contacts
		(scope, bot, phone, name, push_name, business_name, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope, bot, phone) DO UPDATE SET
			name = CASE WHEN excluded.name <> ''
				THEN excluded.name ELSE bot_contacts.name END,
			push_name = CASE WHEN excluded.push_name <> ''
				THEN excluded.push_name ELSE bot_contacts.push_name END,
			business_name = CASE WHEN excluded.business_name <> ''
				THEN excluded.business_name ELSE bot_contacts.business_name END,
			updated_at = excluded.updated_at`,
		entry.Scope, entry.Bot, entry.Phone, entry.Name,
		entry.PushName, entry.BusinessName, formatTime(entry.UpdatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: put address-book entry: %w", err)
	}
	return nil
}

func (s *AddressBookStore) List(
	ctx context.Context, scope, bot string, limit int,
) ([]botflota.AddressBookEntry, error) {
	query := "SELECT " + addressBookColumns + " FROM bot_contacts" +
		" WHERE scope = ? AND bot = ? ORDER BY updated_at DESC, phone ASC"
	args := []any{scope, bot}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list address-book entries: %w", err)
	}
	defer rows.Close()
	entries := make([]botflota.AddressBookEntry, 0)
	for rows.Next() {
		var (
			entry     botflota.AddressBookEntry
			updatedAt string
		)
		if err := rows.Scan(&entry.Scope, &entry.Bot, &entry.Phone,
			&entry.Name, &entry.PushName, &entry.BusinessName,
			&updatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan address-book entry: %w", err)
		}
		entry.UpdatedAt = parseTime(updatedAt)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list address-book entries: %w", err)
	}
	return entries, nil
}
