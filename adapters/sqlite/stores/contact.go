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

// ContactStore records one person's durable identity per scope (ADR 0029).
type ContactStore struct{ db dbtx }

func NewContactStore(db *sql.DB) *ContactStore { return &ContactStore{db: db} }

// contactColumns are the stored identity columns; contactReadColumns adds the
// two derived flags a reader sees, so they always describe the same row.
const contactColumns = `id, scope, phone, display_name, name_updated_at, first_seen, last_seen`

const contactReadColumns = `c.id, c.scope, c.phone, c.display_name, c.name_updated_at,
	c.first_seen, c.last_seen,
	EXISTS (SELECT 1 FROM bot_contacts b
		WHERE b.scope = c.scope AND b.phone = c.phone),
	EXISTS (SELECT 1 FROM chats ch
		WHERE ch.scope = c.scope AND ch.contact_id = c.id)`

// EnsureContact creates an identity without a first inbound message, so the
// address book, an imported history row, and an API-origin send never consume
// the first-contact hook. An existing identity is returned unchanged.
func (s *ContactStore) EnsureContact(
	ctx context.Context, scope, phone string, at time.Time,
) (*botflota.Contact, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO contacts (scope, phone) VALUES (?, ?)
		 ON CONFLICT (scope, phone) DO NOTHING`,
		scope, phone)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ensure contact: %w", err)
	}
	return s.Get(ctx, scope, phone)
}

// RecordInbound records a live inbound message on an identity, creating it
// when missing, and reports whether this was the person's first inbound in the
// scope: the first-contact hook (ADR 0029, ADR 0045). One statement both
// claims first_seen and moves the rest, so a failure cannot leave the claim
// without the caller learning it: first_seen is set only once, a later message
// — or one that arrived after an import — never reports first again, while
// last_seen always moves. A claim and a message that share the same timestamp
// report first twice, which one phone cannot realistically produce.
func (s *ContactStore) RecordInbound(
	ctx context.Context, scope, phone, name string, at time.Time,
) (*botflota.Contact, bool, error) {
	nameAt := ""
	if name != "" {
		nameAt = formatTime(at)
	}
	row := s.db.QueryRowContext(ctx,
		`INSERT INTO contacts (scope, phone, display_name, name_updated_at,
		 first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (scope, phone) DO UPDATE SET
		   first_seen = CASE WHEN contacts.first_seen = ''
		     THEN excluded.first_seen ELSE contacts.first_seen END,
		   last_seen = excluded.last_seen,
		   display_name = CASE WHEN excluded.display_name <> ''
		     THEN excluded.display_name ELSE contacts.display_name END,
		   name_updated_at = CASE
		     WHEN excluded.display_name <> ''
		       AND excluded.display_name <> contacts.display_name
		     THEN excluded.name_updated_at ELSE contacts.name_updated_at END
		 RETURNING `+contactColumns,
		scope, phone, name, nameAt, formatTime(at), formatTime(at))
	contact, err := scanContact(row)
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: record contact message: %w", err)
	}
	return contact, contact.FirstSeen.Equal(at.UTC()), nil
}

func (s *ContactStore) SetDisplayName(
	ctx context.Context, scope, phone, name string, at time.Time,
) error {
	if name == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE contacts SET display_name = ?, name_updated_at = ?
		 WHERE scope = ? AND phone = ? AND display_name <> ?`,
		name, formatTime(at), scope, phone, name)
	if err != nil {
		return fmt.Errorf("sqlite: set contact display name: %w", err)
	}
	return nil
}

func (s *ContactStore) Get(
	ctx context.Context, scope, phone string,
) (*botflota.Contact, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+contactReadColumns+" FROM contacts c WHERE c.scope = ? AND c.phone = ?",
		scope, phone)
	contact, err := scanContactRead(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, botflota.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get contact: %w", err)
	}
	return contact, nil
}

// List returns up to limit identities of one scope, most recently seen first.
// The keyset cursor is the previous page's last row, so a page never skips a
// row; activity moving between pages can still surface a person twice, which
// the read API accepts for a phone-book listing (ADR 0044).
func (s *ContactStore) List(
	ctx context.Context, scope string, since time.Time, limit int, cursor string,
) (botflota.ContactPage, error) {
	if limit <= 0 {
		return botflota.ContactPage{}, nil
	}
	query := "SELECT " + contactReadColumns + " FROM contacts c WHERE c.scope = ?"
	args := []any{scope}
	if !since.IsZero() {
		query += " AND c.first_seen >= ?"
		args = append(args, formatTime(since))
	}
	if cursor != "" {
		lastSeen, id, err := decodeContactCursor(cursor)
		if err != nil {
			return botflota.ContactPage{}, err
		}
		query += " AND (c.last_seen < ? OR (c.last_seen = ? AND c.id < ?))"
		args = append(args, formatTime(lastSeen), formatTime(lastSeen), id)
	}
	// One extra row tells the caller whether another page follows.
	query += " ORDER BY c.last_seen DESC, c.id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return botflota.ContactPage{}, fmt.Errorf("sqlite: list contacts: %w", err)
	}
	defer rows.Close()
	var contacts []botflota.Contact
	for rows.Next() {
		contact, err := scanContactRead(rows)
		if err != nil {
			return botflota.ContactPage{}, err
		}
		contacts = append(contacts, *contact)
	}
	if err := rows.Err(); err != nil {
		return botflota.ContactPage{}, err
	}
	page := botflota.ContactPage{Contacts: contacts}
	if len(contacts) > limit {
		page.Contacts = contacts[:limit]
		last := page.Contacts[limit-1]
		page.NextCursor = encodeContactCursor(last.LastSeen, last.ID)
	}
	return page, nil
}

func scanContact(row interface{ Scan(...any) error }) (*botflota.Contact, error) {
	var (
		contact                     botflota.Contact
		nameAt, firstSeen, lastSeen string
	)
	if err := row.Scan(&contact.ID, &contact.Scope, &contact.Phone,
		&contact.DisplayName, &nameAt, &firstSeen, &lastSeen); err != nil {
		return nil, err
	}
	contact.NameUpdatedAt = parseTime(nameAt)
	contact.FirstSeen = parseTime(firstSeen)
	contact.LastSeen = parseTime(lastSeen)
	return &contact, nil
}

func scanContactRead(row interface{ Scan(...any) error }) (*botflota.Contact, error) {
	var (
		contact                     botflota.Contact
		nameAt, firstSeen, lastSeen string
	)
	if err := row.Scan(&contact.ID, &contact.Scope, &contact.Phone,
		&contact.DisplayName, &nameAt, &firstSeen, &lastSeen,
		&contact.InAddressBook, &contact.HasChat); err != nil {
		return nil, err
	}
	contact.NameUpdatedAt = parseTime(nameAt)
	contact.FirstSeen = parseTime(firstSeen)
	contact.LastSeen = parseTime(lastSeen)
	return &contact, nil
}

// The cursor is opaque to callers: the last row's activity and id, encoded so
// a page cannot be hand-built from a row a caller can see (ADR 0044).
func encodeContactCursor(lastSeen time.Time, id int64) string {
	raw := formatTime(lastSeen) + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeContactCursor(cursor string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	at := strings.LastIndexByte(string(raw), '|')
	if at < 0 {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	// An identity no inbound touched has no last_seen, so the cursor's
	// timestamp is empty and decodes to the zero time.
	var lastSeen time.Time
	if encoded := string(raw[:at]); encoded != "" {
		lastSeen, err = time.Parse(time.RFC3339Nano, encoded)
		if err != nil {
			return time.Time{}, 0, botflota.ErrInvalidCursor
		}
	}
	id, err := strconv.ParseInt(string(raw[at+1:]), 10, 64)
	if err != nil || id < 0 {
		return time.Time{}, 0, botflota.ErrInvalidCursor
	}
	return lastSeen, id, nil
}
