package stores_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
)

// TestAddressBookStoreMergesNames covers the contact import: a name field is
// only replaced by a non-empty one, so a push name never erases the
// address-book name, and the list is newest first.
func TestAddressBookStoreMergesNames(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, entry := range []*botflota.AddressBookEntry{
		{Scope: "s", Bot: "b", Phone: "52155", Name: "Ana", UpdatedAt: t0},
		{Scope: "s", Bot: "b", Phone: "52155", PushName: "anita", UpdatedAt: t1},
		{Scope: "s", Bot: "b", Phone: "52166", PushName: "Beto", UpdatedAt: t2},
	} {
		if err := db.AddressBook().Put(ctx, entry); err != nil {
			t.Fatalf("Put(%s): %v", entry.Phone, err)
		}
	}
	entries, err := db.AddressBook().List(ctx, "s", "b", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want two", entries)
	}
	if entries[0].Phone != "52166" {
		t.Fatalf("first entry = %s, want the newest first", entries[0].Phone)
	}
	if entries[1].Name != "Ana" || entries[1].PushName != "anita" {
		t.Fatalf("merged entry = %+v, want the book name kept", entries[1])
	}
	if err := db.AddressBook().Merge(ctx, &botflota.AddressBookEntry{
		Scope: "s", Bot: "b", Phone: "52177", PushName: "Unknown",
		UpdatedAt: t2,
	}); err != nil {
		t.Fatalf("Merge unknown: %v", err)
	}
	entries, err = db.AddressBook().List(ctx, "s", "b", 0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("Merge created membership: entries %+v, err %v", entries, err)
	}
	if err := db.AddressBook().Merge(ctx, &botflota.AddressBookEntry{
		Scope: "s", Bot: "b", Phone: "52155", PushName: "new push",
		UpdatedAt: t2,
	}); err != nil {
		t.Fatalf("Merge existing: %v", err)
	}
	entries, err = db.AddressBook().List(ctx, "s", "b", 0)
	if err != nil || entries[0].PushName != "new push" {
		t.Fatalf("Merge did not update existing membership: %+v, err %v", entries, err)
	}
	if got, err := db.AddressBook().List(ctx, "s", "b", 1); err != nil || len(got) != 1 {
		t.Fatalf("List with a limit = %+v, %v", got, err)
	}
}
