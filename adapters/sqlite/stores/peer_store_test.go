package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func TestContactEnsureCreatesWithoutAFirstMessage(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Peers().Get(ctx, "support", waKey("52155")); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	contact, err := db.Peers().EnsurePeer(ctx, "support", waKey("52155"), t0)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if contact.ID == 0 || contact.Key.Subject != "52155" {
		t.Fatalf("contact = %#v", contact)
	}
	if !contact.FirstSeen.IsZero() || !contact.LastSeen.IsZero() {
		t.Fatalf("an import or API send must not set first/last seen: %#v", contact)
	}
	again, err := db.Peers().EnsurePeer(ctx, "support", waKey("52155"), t1)
	if err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if again.ID != contact.ID || !again.FirstSeen.IsZero() {
		t.Fatalf("Ensure must be a get-or-create: %#v", again)
	}
}

func TestContactRecordMessageSetsFirstSeenOnce(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	first, isFirst, err := db.Peers().RecordInbound(ctx, "support", waKey("52155"), "Ana", t0)
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if !isFirst {
		t.Fatal("the first message must report first")
	}
	if !first.FirstSeen.Equal(t0) || !first.LastSeen.Equal(t0) {
		t.Fatalf("first_seen = %v, last_seen = %v, want %v",
			first.FirstSeen, first.LastSeen, t0)
	}
	got, isFirst, err := db.Peers().RecordInbound(ctx, "support", waKey("52155"), "", t1)
	if err != nil {
		t.Fatalf("RecordMessage empty name: %v", err)
	}
	if isFirst {
		t.Fatal("a later message must not report first")
	}
	if got.ID != first.ID || !got.FirstSeen.Equal(t0) || !got.LastSeen.Equal(t1) {
		t.Fatalf("contact = %#v, want first %v and last %v", got, t0, t1)
	}
	if got.DisplayName != "Ana" {
		t.Fatalf("display_name = %q, want the kept %q", got.DisplayName, "Ana")
	}
	renamed, _, err := db.Peers().RecordInbound(ctx, "support", waKey("52155"), "Ana B", t2)
	if err != nil {
		t.Fatalf("RecordMessage new name: %v", err)
	}
	if renamed.DisplayName != "Ana B" || !renamed.NameUpdatedAt.Equal(t2) {
		t.Fatalf("rename = %#v, want Ana B stamped at %v", renamed, t2)
	}
}

// TestContactFirstMessageAfterAnImport covers the first-contact hook on an
// identity the address book or an API send created first (ADR 0029, ADR 0045).
func TestContactFirstMessageAfterAnImport(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Peers().EnsurePeer(ctx, "support", waKey("52155"), t0); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	contact, isFirst, err := db.Peers().RecordInbound(ctx, "support", waKey("52155"), "Ana", t1)
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if !isFirst {
		t.Fatal("the first inbound after an import must report first")
	}
	if !contact.FirstSeen.Equal(t1) {
		t.Fatalf("first_seen = %v, want the inbound time %v", contact.FirstSeen, t1)
	}
	_, isFirst, err = db.Peers().RecordInbound(ctx, "support", waKey("52155"), "", t2)
	if err != nil || isFirst {
		t.Fatalf("a second inbound = (first %v, err %v), want (false, nil)", isFirst, err)
	}
}

// TestContactFlagsAreDerived covers the address-book, chat, and sync-history
// relations without storing those flags on the identity row.
func TestContactFlagsAreDerived(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	contact, err := db.Peers().EnsurePeer(ctx, "support", waKey("52155"), t0)
	if err != nil {
		t.Fatalf("EnsurePeer: %v", err)
	}
	got, err := db.Peers().Get(ctx, "support", waKey("52155"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AddressBookImported || got.HasChat || got.HasHistory {
		t.Fatalf("flags = %#v, want both off before any relation", got)
	}
	if err := db.Directory().Put(ctx, &botflota.DirectoryEntry{
		Scope: "support", Bot: "support-bot", PeerKey: waKey("52155"),
		SourceIdentity: waKey("52155"), DisplayName: "Ana", UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Put address-book entry: %v", err)
	}
	if _, err := db.Chats().Ensure(ctx, "support", "support-bot", waChatTarget,
		contact.ID, t0); err != nil {
		t.Fatalf("Ensure chat: %v", err)
	}
	got, err = db.Peers().Get(ctx, "support", waKey("52155"))
	if err != nil {
		t.Fatalf("Get after relations: %v", err)
	}
	if !got.AddressBookImported || !got.HasChat || got.HasHistory {
		t.Fatalf("flags = %#v, want imported with a chat and no history", got)
	}
	chat, err := db.Chats().ForTarget(ctx, "support", "support-bot", waChatTarget)
	if err != nil {
		t.Fatalf("ForTarget: %v", err)
	}
	if err := db.Messages().Append(ctx, &botflota.Message{
		ChatID: chat.ID, Target: chat.Target, Direction: botflota.Inbound,
		Content: botflota.TextContent{Body: "before pairing"}, At: t0,
		Ingest: botflota.IngestSync,
	}); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	got, err = db.Peers().Get(ctx, "support", waKey("52155"))
	if err != nil || !got.HasHistory || !got.FirstSeen.IsZero() {
		t.Fatalf("history contact = %#v, err %v; want history, no live inbound", got, err)
	}
	// A book entry for another scope must not mark this identity.
	if err := db.Directory().Put(ctx, &botflota.DirectoryEntry{
		Scope: "other", Bot: "support-bot", PeerKey: waKey("52155"),
		SourceIdentity: waKey("52155"), UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("Put other entry: %v", err)
	}
	people, err := db.Peers().List(ctx, "other", time.Time{}, 10, "")
	if err != nil {
		t.Fatalf("List other: %v", err)
	}
	if len(people.Peers) != 0 {
		t.Fatalf("other scope = %#v, want no identity", people.Peers)
	}
}

// TestContactSameTimestampReportsFirstOnce: the claim is a conditional update,
// not a comparison of timestamps, so a clock with no resolution cannot report
// two first messages (ADR 0029).
func TestContactSameTimestampReportsFirstOnce(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	contact, isFirst, err := db.Peers().RecordInbound(ctx, "support", waKey("52155"), "Ana", t0)
	if err != nil {
		t.Fatalf("first RecordInbound: %v", err)
	}
	if !isFirst {
		t.Fatal("the first message must report first")
	}
	again, isFirst, err := db.Peers().RecordInbound(ctx, "support", waKey("52155"), "Ana", t0)
	if err != nil {
		t.Fatalf("second RecordInbound: %v", err)
	}
	if isFirst {
		t.Fatalf("a second inbound at the same timestamp %v reported first again", t0)
	}
	if again.ID != contact.ID || !again.FirstSeen.Equal(t0) {
		t.Fatalf("contact = %#v, want the first identity and first_seen %v", again, t0)
	}
}

// TestContactListPagesAnIdentityThatNeverWrote covers the cursor for an
// address-book-only identity: its last_seen is empty, and the cursor the list
// hands back must round-trip instead of failing the next page.
func TestContactListPagesAnIdentityThatNeverWrote(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, phone := range []string{"5215500000001", "5215500000002"} {
		if _, err := db.Peers().EnsurePeer(ctx, "support", waKey(phone), t0); err != nil {
			t.Fatalf("EnsurePeer %s: %v", phone, err)
		}
	}
	page, err := db.Peers().List(ctx, "support", time.Time{}, 1, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Peers) != 1 || page.NextCursor == "" {
		t.Fatalf("page = %#v, want one row and a cursor", page)
	}
	next, err := db.Peers().List(ctx, "support", time.Time{}, 1, page.NextCursor)
	if err != nil {
		t.Fatalf("next page with an empty last_seen: %v", err)
	}
	if len(next.Peers) != 1 || next.NextCursor != "" {
		t.Fatalf("next = %#v, want the last row and no cursor", next)
	}
}

// TestContactSetDisplayName covers names learned from channel contact data:
// the name changes without touching first or last seen, and a missing identity
// is a no-op.
func TestContactSetDisplayName(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	contact, _, err := db.Peers().RecordInbound(ctx, "support", waKey("52155"), "", t0)
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if err := db.Peers().SetDisplayName(ctx, "support", waKey("52155"), "Ana", t2); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	got, err := db.Peers().Get(ctx, "support", waKey("52155"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != contact.ID || got.DisplayName != "Ana" || !got.NameUpdatedAt.Equal(t2) {
		t.Fatalf("contact = %#v, want id %d named Ana at %v", got, contact.ID, t2)
	}
	if !got.FirstSeen.Equal(t0) || !got.LastSeen.Equal(t0) {
		t.Fatalf("contact times moved: %v..%v, want %v", got.FirstSeen, got.LastSeen, t0)
	}
	if err := db.Peers().SetDisplayName(ctx, "support", waKey("nobody"), "X", t2); err != nil {
		t.Fatalf("SetDisplayName on a missing identity: %v", err)
	}
	if err := db.Peers().SetDisplayName(ctx, "support", waKey("52155"), "Stale", t2); err != nil {
		t.Fatalf("SetDisplayName over known name: %v", err)
	}
	got, err = db.Peers().Get(ctx, "support", waKey("52155"))
	if err != nil || got.DisplayName != "Ana" || !got.NameUpdatedAt.Equal(t2) {
		t.Fatalf("existing display name changed: contact %#v, err %v", got, err)
	}
}

func TestContactIsScoped(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	first, _, err := db.Peers().RecordInbound(ctx, "a", waKey("52155"), "Ana", t0)
	if err != nil {
		t.Fatalf("RecordMessage a: %v", err)
	}
	second, _, err := db.Peers().RecordInbound(ctx, "b", waKey("52155"), "Ana", t0)
	if err != nil {
		t.Fatalf("RecordMessage b: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("one phone in two scopes must be two identities")
	}
}

// TestContactListPagesByLastSeen: the scope read is ordered by activity, the
// since filter looks at first_seen, and the cursor pages without repeating a
// row (ADR 0044).
func TestContactListPagesByLastSeen(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	t3 := t2.Add(5 * time.Minute)
	for _, contact := range []struct {
		phone, name string
		first, last time.Time
	}{
		{"5215500000003", "Cora", t0, t0},
		{"5215500000002", "Beto", t1, t1},
		{"5215500000001", "Ana", t1, t3},
	} {
		if _, _, err := db.Peers().RecordInbound(ctx, "support",
			waKey(contact.phone), contact.name, contact.first); err != nil {
			t.Fatalf("RecordMessage %s: %v", contact.phone, err)
		}
		if !contact.last.Equal(contact.first) {
			if _, _, err := db.Peers().RecordInbound(ctx, "support",
				waKey(contact.phone), "", contact.last); err != nil {
				t.Fatalf("touch %s: %v", contact.phone, err)
			}
		}
	}
	if _, _, err := db.Peers().RecordInbound(ctx, "other", waKey("5215599999999"),
		"Zoe", t0); err != nil {
		t.Fatalf("RecordMessage other scope: %v", err)
	}

	page, err := db.Peers().List(ctx, "support", time.Time{}, 2, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Peers) != 2 ||
		page.Peers[0].Key.Subject != "5215500000001" ||
		page.Peers[1].Key.Subject != "5215500000002" {
		t.Fatalf("page one = %#v", page.Peers)
	}
	if page.NextCursor == "" {
		t.Fatal("a full page must carry a cursor")
	}

	next, err := db.Peers().List(ctx, "support", time.Time{}, 2, page.NextCursor)
	if err != nil {
		t.Fatalf("next page: %v", err)
	}
	if len(next.Peers) != 1 || next.Peers[0].Key.Subject != "5215500000003" {
		t.Fatalf("page two = %#v", next.Peers)
	}
	if next.NextCursor != "" {
		t.Fatalf("last page cursor = %q, want none", next.NextCursor)
	}

	// since filters by first_seen, so the active identity created before it is
	// out even though its activity is the newest.
	since, err := db.Peers().List(ctx, "support", t1, 10, "")
	if err != nil {
		t.Fatalf("List since: %v", err)
	}
	if len(since.Peers) != 2 ||
		since.Peers[0].Key.Subject != "5215500000001" ||
		since.Peers[1].Key.Subject != "5215500000002" {
		t.Fatalf("since page = %#v", since.Peers)
	}

	_, err = db.Peers().List(ctx, "support", time.Time{}, 2, "notacursor")
	if !errors.Is(err, botflota.ErrInvalidCursor) {
		t.Fatalf("bad cursor error = %v, want ErrInvalidCursor", err)
	}
}
