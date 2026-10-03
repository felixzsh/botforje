package ingest

import (
	"context"
	"testing"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// TestContactImporterKeepsTheAddressBookName covers the contact import: the
// address-book name and the push name live in separate fields, so a later
// push name never erases the name the phone's address book holds.
func TestContactImporterKeepsTheAddressBookName(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := NewAddressBook("dev", "bot", db.Contacts(), db.AddressBook(), nil, nil)
	importer.Handle(ctx, &events.Contact{
		JID: types.NewJID("5215500000000", types.DefaultUserServer),
		Action: &waSyncAction.ContactAction{
			FullName: proto.String("Ana Perez"),
		},
		Timestamp: t0,
	})
	importer.Handle(ctx, &events.PushName{
		JID:         types.NewJID("5215500000000", types.DefaultUserServer),
		NewPushName: "anita",
	})
	contacts, err := db.AddressBook().List(ctx, "dev", "bot", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(contacts) != 1 {
		t.Fatalf("contacts = %+v, want one", contacts)
	}
	if contacts[0].Name != "Ana Perez" || contacts[0].PushName != "anita" {
		t.Fatalf("contact = %+v", contacts[0])
	}
	if contacts[0].DisplayName() != "Ana Perez" {
		t.Fatalf("display name = %q, want the address-book name", contacts[0].DisplayName())
	}
	// The imported phone is a contact the directory knows, marked as booked
	// and without a first message of its own.
	contact, err := db.Contacts().Get(ctx, "dev", "5215500000000")
	if err != nil {
		t.Fatalf("Get contact: %v", err)
	}
	if !contact.InAddressBook || contact.HasChat || !contact.FirstSeen.IsZero() {
		t.Fatalf("contact = %#v, want booked, no chat, never messaged", contact)
	}
}

// TestContactImporterResolvesALID checks the contact import stores the phone
// number, not the hidden user id (ADR 0020).
func TestContactImporterResolvesALID(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	lid := types.NewJID("162405085098836", types.HiddenUserServer)
	lookup := func(got types.JID) (types.JID, bool) {
		if got != lid {
			return types.JID{}, false
		}
		return types.NewJID("5215500000000", types.DefaultUserServer), true
	}
	importer := NewAddressBook("dev", "bot", db.Contacts(), db.AddressBook(), lookup, nil)
	importer.Handle(ctx, &events.PushName{JID: lid, NewPushName: "Ana"})
	contacts, err := db.AddressBook().List(ctx, "dev", "bot", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(contacts) != 1 || contacts[0].Phone != "5215500000000" {
		t.Fatalf("contacts = %+v, want the phone number", contacts)
	}
}
