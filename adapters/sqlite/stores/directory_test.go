package stores_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// TestDirectoryStoreMergesNames covers the directory import: an address-book
// name replaces a stored one, a later push or business name only fills a gap,
// and the list is newest first (ADR 0037, ADR 0051).
func TestDirectoryStoreMergesNames(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	source := waKey("5215500000000")
	entry := func(subject, name string, at time.Time) *botflota.DirectoryEntry {
		return &botflota.DirectoryEntry{
			Scope: "s", Bot: "b", PeerKey: waKey(subject),
			SourceIdentity: source, DisplayName: name, UpdatedAt: at,
		}
	}
	if err := db.Directory().Put(ctx, entry("52155", "Ana", t0)); err != nil {
		t.Fatalf("Put Ana: %v", err)
	}
	if err := db.Directory().Merge(ctx, entry("52155", "anita", t1)); err != nil {
		t.Fatalf("Merge push name: %v", err)
	}
	if err := db.Directory().Put(ctx, entry("52166", "Beto", t2)); err != nil {
		t.Fatalf("Put Beto: %v", err)
	}
	entries, err := db.Directory().List(ctx, "s", "b", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want two", entries)
	}
	if entries[0].PeerKey.Subject != "52166" {
		t.Fatalf("first entry = %s, want the newest first", entries[0].PeerKey.Subject)
	}
	if entries[1].DisplayName != "Ana" {
		t.Fatalf("merged entry = %+v, want the book name kept", entries[1])
	}
	if entries[1].SourceIdentity != source {
		t.Fatalf("source identity = %+v, want %+v", entries[1].SourceIdentity, source)
	}
	if err := db.Directory().Merge(ctx, entry("52177", "Unknown", t2)); err != nil {
		t.Fatalf("Merge unknown: %v", err)
	}
	entries, err = db.Directory().List(ctx, "s", "b", 0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("Merge created membership: entries %+v, err %v", entries, err)
	}
	// A gap is filled: the name arrives after the membership did.
	if err := db.Directory().Put(ctx, entry("52188", "", t0)); err != nil {
		t.Fatalf("Put without a name: %v", err)
	}
	if err := db.Directory().Merge(ctx, entry("52188", "Cora", t2)); err != nil {
		t.Fatalf("Merge into a gap: %v", err)
	}
	entries, err = db.Directory().List(ctx, "s", "b", 0)
	if err != nil || directoryName(t, entries, "52188") != "Cora" {
		t.Fatalf("gap not filled: %+v, err %v", entries, err)
	}
	if got, err := db.Directory().List(ctx, "s", "b", 1); err != nil || len(got) != 1 {
		t.Fatalf("List with a limit = %+v, %v", got, err)
	}
	// Snapshots from two source accounts stay independent memberships.
	other := entry("52155", "Ana", t1)
	other.SourceIdentity = waKey("5215599999999")
	if err := db.Directory().Put(ctx, other); err != nil {
		t.Fatalf("Put second source: %v", err)
	}
	entries, err = db.Directory().List(ctx, "s", "b", 0)
	if err != nil {
		t.Fatalf("List after second source: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want the two sources kept apart", len(entries))
	}
}

// directoryName reads one entry's normalized name out of a list read.
func directoryName(t *testing.T, entries []botflota.DirectoryEntry, subject string) string {
	t.Helper()
	for _, entry := range entries {
		if entry.PeerKey.Subject == subject {
			return entry.DisplayName
		}
	}
	t.Fatalf("no directory entry for %s", subject)
	return ""
}
