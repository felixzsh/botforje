package stores

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/felixzsh/botflota"
)

// DirectoryStore records the imported account-directory entries of one bot,
// keyed by owner, source identity, and peer identity (ADR 0037, ADR 0051).
type DirectoryStore struct{ db *sql.DB }

func NewDirectoryStore(db *sql.DB) *DirectoryStore {
	return &DirectoryStore{db: db}
}

const directoryColumns = `scope, bot, provider, subject,
	source_provider, source_subject, display_name, updated_at`

func (s *DirectoryStore) Put(
	ctx context.Context, entry *botflota.DirectoryEntry,
) error {
	if !directoryEntryKeyed(entry) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO directory_entries
		(scope, bot, provider, subject, source_provider, source_subject,
		 display_name, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (scope, bot, provider, subject, source_provider, source_subject)
		DO UPDATE SET
			display_name = CASE WHEN excluded.display_name <> ''
				THEN excluded.display_name ELSE directory_entries.display_name END,
			updated_at = excluded.updated_at`,
		entry.Scope, entry.Bot, entry.PeerKey.Provider, entry.PeerKey.Subject,
		entry.SourceIdentity.Provider, entry.SourceIdentity.Subject,
		entry.DisplayName, formatTime(entry.UpdatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: put directory entry: %w", err)
	}
	return nil
}

// Merge fills a missing name for an existing directory member without
// creating membership, so a later push or business name never replaces the
// name the account's own book supplied (ADR 0051).
func (s *DirectoryStore) Merge(
	ctx context.Context, entry *botflota.DirectoryEntry,
) error {
	if !directoryEntryKeyed(entry) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE directory_entries SET
		display_name = CASE WHEN display_name = '' AND ? <> ''
			THEN ? ELSE display_name END,
		updated_at = ?
		WHERE scope = ? AND bot = ? AND provider = ? AND subject = ?
		AND source_provider = ? AND source_subject = ?`,
		entry.DisplayName, entry.DisplayName, formatTime(entry.UpdatedAt),
		entry.Scope, entry.Bot, entry.PeerKey.Provider, entry.PeerKey.Subject,
		entry.SourceIdentity.Provider, entry.SourceIdentity.Subject)
	if err != nil {
		return fmt.Errorf("sqlite: merge directory entry: %w", err)
	}
	return nil
}

func (s *DirectoryStore) List(
	ctx context.Context, scope, bot string, limit int,
) ([]botflota.DirectoryEntry, error) {
	query := "SELECT " + directoryColumns + " FROM directory_entries" +
		" WHERE scope = ? AND bot = ?" +
		" ORDER BY updated_at DESC, provider ASC, subject ASC"
	args := []any{scope, bot}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list directory entries: %w", err)
	}
	defer rows.Close()
	entries := make([]botflota.DirectoryEntry, 0)
	for rows.Next() {
		entry, err := scanDirectory(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan directory entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list directory entries: %w", err)
	}
	return entries, nil
}

// directoryEntryKeyed reports whether an entry carries the complete key:
// owner, peer identity, and source identity.
func directoryEntryKeyed(entry *botflota.DirectoryEntry) bool {
	if entry == nil || entry.Scope == "" || entry.Bot == "" {
		return false
	}
	if !entry.PeerKey.Valid() || !entry.SourceIdentity.Valid() {
		return false
	}
	return entry.PeerKey.Provider == entry.SourceIdentity.Provider
}
