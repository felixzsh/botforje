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

// contactReadColumns are the stored identity columns plus derived reader flags.
const contactReadColumns = `c.id, c.scope, c.phone, c.display_name, c.name_updated_at,
	c.first_seen, c.last_seen,
	EXISTS (SELECT 1 FROM bot_contacts b
		WHERE b.scope = c.scope AND b.phone = c.phone),
	EXISTS (SELECT 1 FROM chats ch
		WHERE ch.scope = c.scope AND ch.contact_id = c.id),
	EXISTS (SELECT 1 FROM chats ch JOIN messages m ON m.chat_id = ch.id
		WHERE ch.scope = c.scope AND ch.contact_id = c.id
		AND m.ingest_kind = 'sync')`

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
	return s.get(ctx, s.db, scope, phone)
}

// RecordInbound records a live inbound message on an identity, creating it
// when missing, and reports whether this was the person's first inbound in the
// scope: the first-contact hook (ADR 0029, ADR 0045). The claim of first_seen
// is a conditional update whose affected rows are the answer, so two messages
// that share a timestamp still report first only once. The claim, the
// activity, and the name land in one transaction — its own when this store
// holds a connection, the caller's turn when it was composed into one — so a
// failure rolls the claim back and the next message reports first again.
func (s *ContactStore) RecordInbound(
	ctx context.Context, scope, phone, name string, at time.Time,
) (*botflota.Contact, bool, error) {
	if db, ok := s.db.(*sql.DB); ok {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, false, fmt.Errorf("sqlite: begin contact message: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		contact, first, err := s.recordInbound(ctx, tx, scope, phone, name, at)
		if err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("sqlite: commit contact message: %w", err)
		}
		return contact, first, nil
	}
	return s.recordInbound(ctx, s.db, scope, phone, name, at)
}

func (s *ContactStore) recordInbound(
	ctx context.Context, q dbtx, scope, phone, name string, at time.Time,
) (*botflota.Contact, bool, error) {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO contacts (scope, phone) VALUES (?, ?)
		 ON CONFLICT (scope, phone) DO NOTHING`,
		scope, phone); err != nil {
		return nil, false, fmt.Errorf("sqlite: ensure contact for inbound: %w", err)
	}
	// The claim is the answer: a row updated here was the first empty
	// first_seen this phone had, whatever its timestamp.
	result, err := q.ExecContext(ctx,
		`UPDATE contacts SET first_seen = ?
		 WHERE scope = ? AND phone = ? AND first_seen = ''`,
		formatTime(at), scope, phone)
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: claim first contact: %w", err)
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("sqlite: claim first contact: %w", err)
	}
	nameAt := ""
	if name != "" {
		nameAt = formatTime(at)
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE contacts SET
		   last_seen = ?,
		   display_name = CASE WHEN ? <> '' THEN ? ELSE display_name END,
		   name_updated_at = CASE
		     WHEN ? <> '' AND ? <> display_name THEN ? ELSE name_updated_at END
		 WHERE scope = ? AND phone = ?`,
		formatTime(at), name, name, name, name, nameAt, scope, phone); err != nil {
		return nil, false, fmt.Errorf("sqlite: update contact message: %w", err)
	}
	contact, err := s.get(ctx, q, scope, phone)
	if err != nil {
		return nil, false, err
	}
	return contact, claimed == 1, nil
}

func (s *ContactStore) SetDisplayName(
	ctx context.Context, scope, phone, name string, at time.Time,
) error {
	if name == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE contacts SET display_name = ?, name_updated_at = ?
		 WHERE scope = ? AND phone = ? AND display_name = ''`,
		name, formatTime(at), scope, phone)
	if err != nil {
		return fmt.Errorf("sqlite: set contact display name: %w", err)
	}
	return nil
}

func (s *ContactStore) Get(
	ctx context.Context, scope, phone string,
) (*botflota.Contact, error) {
	return s.get(ctx, s.db, scope, phone)
}

func (s *ContactStore) get(
	ctx context.Context, q dbtx, scope, phone string,
) (*botflota.Contact, error) {
	row := q.QueryRowContext(ctx,
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

func scanContactRead(row interface{ Scan(...any) error }) (*botflota.Contact, error) {
	var (
		contact                     botflota.Contact
		nameAt, firstSeen, lastSeen string
	)
	if err := row.Scan(&contact.ID, &contact.Scope, &contact.Phone,
		&contact.DisplayName, &nameAt, &firstSeen, &lastSeen,
		&contact.AddressBookImported, &contact.HasChat, &contact.HasHistory); err != nil {
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
