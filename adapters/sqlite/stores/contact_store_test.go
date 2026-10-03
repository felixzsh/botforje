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
	if _, err := db.Contacts().Get(ctx, "support", "52155"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	contact, err := db.Contacts().EnsureContact(ctx, "support", "52155", t0)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if contact.ID == 0 || contact.Phone != "52155" {
		t.Fatalf("contact = %#v", contact)
	}
	if !contact.FirstSeen.IsZero() || !contact.LastSeen.IsZero() {
		t.Fatalf("an import or API send must not set first/last seen: %#v", contact)
	}
	again, err := db.Contacts().EnsureContact(ctx, "support", "52155", t1)
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
	first, isFirst, err := db.Contacts().RecordInbound(ctx, "support", "52155", "Ana", t0)
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
	got, isFirst, err := db.Contacts().RecordInbound(ctx, "support", "52155", "", t1)
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
	renamed, _, err := db.Contacts().RecordInbound(ctx, "support", "52155", "Ana B", t2)
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
	if _, err := db.Contacts().EnsureContact(ctx, "support", "52155", t0); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	contact, isFirst, err := db.Contacts().RecordInbound(ctx, "support", "52155", "Ana", t1)
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if !isFirst {
		t.Fatal("the first inbound after an import must report first")
	}
	if !contact.FirstSeen.Equal(t1) {
		t.Fatalf("first_seen = %v, want the inbound time %v", contact.FirstSeen, t1)
	}
	_, isFirst, err = db.Contacts().RecordInbound(ctx, "support", "52155", "", t2)
	if err != nil || isFirst {
		t.Fatalf("a second inbound = (first %v, err %v), want (false, nil)", isFirst, err)
	}
}

// TestContactSetDisplayName covers names learned from channel contact data:
// the name changes without touching first or last seen, and a missing identity
// is a no-op.
func TestContactSetDisplayName(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	contact, _, err := db.Contacts().RecordInbound(ctx, "support", "52155", "", t0)
	if err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if err := db.Contacts().SetDisplayName(ctx, "support", "52155", "Ana", t2); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	got, err := db.Contacts().Get(ctx, "support", "52155")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != contact.ID || got.DisplayName != "Ana" || !got.NameUpdatedAt.Equal(t2) {
		t.Fatalf("contact = %#v, want id %d named Ana at %v", got, contact.ID, t2)
	}
	if !got.FirstSeen.Equal(t0) || !got.LastSeen.Equal(t0) {
		t.Fatalf("contact times moved: %v..%v, want %v", got.FirstSeen, got.LastSeen, t0)
	}
	if err := db.Contacts().SetDisplayName(ctx, "support", "nobody", "X", t2); err != nil {
		t.Fatalf("SetDisplayName on a missing identity: %v", err)
	}
}

func TestContactIsScoped(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	first, _, err := db.Contacts().RecordInbound(ctx, "a", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("RecordMessage a: %v", err)
	}
	second, _, err := db.Contacts().RecordInbound(ctx, "b", "52155", "Ana", t0)
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
		if _, _, err := db.Contacts().RecordInbound(ctx, "support",
			contact.phone, contact.name, contact.first); err != nil {
			t.Fatalf("RecordMessage %s: %v", contact.phone, err)
		}
		if !contact.last.Equal(contact.first) {
			if _, _, err := db.Contacts().RecordInbound(ctx, "support",
				contact.phone, "", contact.last); err != nil {
				t.Fatalf("touch %s: %v", contact.phone, err)
			}
		}
	}
	if _, _, err := db.Contacts().RecordInbound(ctx, "other", "5215599999999", "Zoe", t0); err != nil {
		t.Fatalf("RecordMessage other scope: %v", err)
	}

	page, err := db.Contacts().List(ctx, "support", time.Time{}, 2, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Contacts) != 2 ||
		page.Contacts[0].Phone != "5215500000001" ||
		page.Contacts[1].Phone != "5215500000002" {
		t.Fatalf("page one = %#v", page.Contacts)
	}
	if page.NextCursor == "" {
		t.Fatal("a full page must carry a cursor")
	}

	next, err := db.Contacts().List(ctx, "support", time.Time{}, 2, page.NextCursor)
	if err != nil {
		t.Fatalf("next page: %v", err)
	}
	if len(next.Contacts) != 1 || next.Contacts[0].Phone != "5215500000003" {
		t.Fatalf("page two = %#v", next.Contacts)
	}
	if next.NextCursor != "" {
		t.Fatalf("last page cursor = %q, want none", next.NextCursor)
	}

	// since filters by first_seen, so the active identity created before it is
	// out even though its activity is the newest.
	since, err := db.Contacts().List(ctx, "support", t1, 10, "")
	if err != nil {
		t.Fatalf("List since: %v", err)
	}
	if len(since.Contacts) != 2 ||
		since.Contacts[0].Phone != "5215500000001" ||
		since.Contacts[1].Phone != "5215500000002" {
		t.Fatalf("since page = %#v", since.Contacts)
	}

	_, err = db.Contacts().List(ctx, "support", time.Time{}, 2, "notacursor")
	if !errors.Is(err, botflota.ErrInvalidCursor) {
		t.Fatalf("bad cursor error = %v, want ErrInvalidCursor", err)
	}
}
