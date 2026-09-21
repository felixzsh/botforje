package stores

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/felixzsh/botflota"
)

type ContactStore struct{ db *sql.DB }

func NewContactStore(db *sql.DB) *ContactStore { return &ContactStore{db: db} }

const contactColumns = `scope, bot, phone, name, push_name, business_name, updated_at`

func (s *ContactStore) Put(ctx context.Context, contact *botflota.Contact) error {
	if contact == nil || contact.Scope == "" || contact.Bot == "" ||
		contact.Phone == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO contacts
		(scope, bot, phone, name, push_name, business_name, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope, bot, phone) DO UPDATE SET
			name = CASE WHEN excluded.name <> ''
				THEN excluded.name ELSE contacts.name END,
			push_name = CASE WHEN excluded.push_name <> ''
				THEN excluded.push_name ELSE contacts.push_name END,
			business_name = CASE WHEN excluded.business_name <> ''
				THEN excluded.business_name ELSE contacts.business_name END,
			updated_at = excluded.updated_at`,
		contact.Scope, contact.Bot, contact.Phone, contact.Name,
		contact.PushName, contact.BusinessName, formatTime(contact.UpdatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: put contact: %w", err)
	}
	return nil
}

func (s *ContactStore) List(
	ctx context.Context, scope, bot string, limit int,
) ([]botflota.Contact, error) {
	query := "SELECT " + contactColumns + " FROM contacts WHERE scope = ? AND bot = ?" +
		" ORDER BY updated_at DESC, phone ASC"
	args := []any{scope, bot}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list contacts: %w", err)
	}
	defer rows.Close()
	contacts := make([]botflota.Contact, 0)
	for rows.Next() {
		var (
			contact   botflota.Contact
			updatedAt string
		)
		if err := rows.Scan(&contact.Scope, &contact.Bot, &contact.Phone,
			&contact.Name, &contact.PushName, &contact.BusinessName,
			&updatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan contact: %w", err)
		}
		contact.UpdatedAt = parseTime(updatedAt)
		contacts = append(contacts, contact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list contacts: %w", err)
	}
	return contacts, nil
}
