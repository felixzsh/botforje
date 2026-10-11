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
// address-book name wins over a later push name, so the phone's own book is
// never erased (ADR 0037, ADR 0051).
func TestContactImporterKeepsTheAddressBookName(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	importer := NewAddressBook("dev", "bot", db.Peers(), db.Directory(), nil, nil)
	importer.SetSource(PeerKey("5215599999999"))
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
	entries, err := db.Directory().List(ctx, "dev", "bot", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want one", entries)
	}
	if entries[0].DisplayName != "Ana Perez" {
		t.Fatalf("display name = %q, want the address-book name", entries[0].DisplayName)
	}
	if entries[0].PeerKey != PeerKey("5215500000000") ||
		entries[0].SourceIdentity != PeerKey("5215599999999") {
		t.Fatalf("entry key = %+v, want the peer and its source account", entries[0])
	}
	// The imported peer is one the directory knows, marked as booked and
	// without a first message of its own.
	peer, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000000"))
	if err != nil {
		t.Fatalf("Get peer: %v", err)
	}
	if !peer.AddressBookImported || peer.HasChat || !peer.FirstSeen.IsZero() {
		t.Fatalf("peer = %#v, want booked, no chat, never messaged", peer)
	}
	if peer.DisplayName != "Ana Perez" {
		t.Fatalf("peer display name = %q, want the imported name", peer.DisplayName)
	}
	importer.Handle(ctx, &events.PushName{
		JID:         types.NewJID("5215500000000", types.DefaultUserServer),
		NewPushName: "newer name",
	})
	peer, err = db.Peers().Get(ctx, "dev", PeerKey("5215500000000"))
	if err != nil || peer.DisplayName != "Ana Perez" {
		t.Fatalf("new push name replaced the known name: %#v, %v", peer, err)
	}
}

// TestContactImporterResolvesALID checks the contact import stores the phone
// number subject, not the hidden user id (ADR 0020).
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
	importer := NewAddressBook("dev", "bot", db.Peers(), db.Directory(), lookup, nil)
	importer.SetSource(PeerKey("5215599999999"))
	importer.Handle(ctx, &events.PushName{JID: lid, NewPushName: "Ana"})
	entries, err := db.Directory().List(ctx, "dev", "bot", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("push name created directory membership: %+v", entries)
	}
	peer, err := db.Peers().Get(ctx, "dev", PeerKey("5215500000000"))
	if err != nil || peer.AddressBookImported || peer.DisplayName != "Ana" {
		t.Fatalf("peer = %#v, err %v; want name but no book membership", peer, err)
	}
}
