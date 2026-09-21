package stores_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
)

// TestContactStoreMergesNames covers the contact import: a name field is only
// replaced by a non-empty one, so a push name never erases the address-book
// name, and the list is newest first.
func TestContactStoreMergesNames(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, contact := range []*botflota.Contact{
		{Scope: "s", Bot: "b", Phone: "52155", Name: "Ana", UpdatedAt: t0},
		{Scope: "s", Bot: "b", Phone: "52155", PushName: "anita", UpdatedAt: t1},
		{Scope: "s", Bot: "b", Phone: "52166", PushName: "Beto", UpdatedAt: t2},
	} {
		if err := db.Contacts().Put(ctx, contact); err != nil {
			t.Fatalf("Put(%s): %v", contact.Phone, err)
		}
	}
	contacts, err := db.Contacts().List(ctx, "s", "b", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(contacts) != 2 {
		t.Fatalf("contacts = %+v, want two", contacts)
	}
	if contacts[0].Phone != "52166" {
		t.Fatalf("first contact = %s, want the newest first", contacts[0].Phone)
	}
	if contacts[1].Name != "Ana" || contacts[1].PushName != "anita" {
		t.Fatalf("merged contact = %+v, want the book name kept", contacts[1])
	}
	if got, err := db.Contacts().List(ctx, "s", "b", 1); err != nil || len(got) != 1 {
		t.Fatalf("List with a limit = %+v, %v", got, err)
	}
}
