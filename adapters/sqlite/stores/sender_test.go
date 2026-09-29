package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func TestSenderUpsertIsGetOrCreate(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Senders().Get(ctx, "support", "52155"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	first, created, err := db.Senders().Upsert(ctx, "support", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !created {
		t.Fatal("Upsert did not report a new profile")
	}
	if first.ID == 0 || first.Phone != "52155" {
		t.Fatalf("profile = %#v", first)
	}
	if !first.FirstSeen.Equal(t0) || !first.LastSeen.Equal(t0) {
		t.Fatalf("first_seen = %v, last_seen = %v, want %v",
			first.FirstSeen, first.LastSeen, t0)
	}
}

func TestSenderUpsertUpdatesProfile(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	sender, _, err := db.Senders().Upsert(ctx, "support", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, created, err := db.Senders().Upsert(ctx, "support", "52155", "", t1)
	if err != nil {
		t.Fatalf("Upsert empty name: %v", err)
	}
	if created {
		t.Fatal("second Upsert reported a new profile")
	}
	if got.ID != sender.ID || !got.FirstSeen.Equal(t0) {
		t.Fatalf("profile = %#v, want id %d since %v", got, sender.ID, t0)
	}
	if got.DisplayName != "Ana" {
		t.Fatalf("display_name = %q, want the kept %q", got.DisplayName, "Ana")
	}
	if !got.LastSeen.Equal(t1) {
		t.Fatalf("last_seen = %v, want %v", got.LastSeen, t1)
	}
	if !got.NameUpdatedAt.Equal(t0) {
		t.Fatalf("name_updated_at = %v, want %v (unchanged name)", got.NameUpdatedAt, t0)
	}
	renamed, _, err := db.Senders().Upsert(ctx, "support", "52155", "Ana B", t2)
	if err != nil {
		t.Fatalf("Upsert new name: %v", err)
	}
	if renamed.DisplayName != "Ana B" || !renamed.NameUpdatedAt.Equal(t2) {
		t.Fatalf("rename = %#v, want Ana B stamped at %v", renamed, t2)
	}
}

// TestSenderSetDisplayName covers names learned from channel contact data: the
// name changes without touching first or last seen, and a missing profile is a
// no-op.
func TestSenderSetDisplayName(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	sender, _, err := db.Senders().Upsert(ctx, "support", "52155", "", t0)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := db.Senders().SetDisplayName(ctx, "support", "52155", "Ana", t2); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	got, err := db.Senders().Get(ctx, "support", "52155")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != sender.ID || got.DisplayName != "Ana" || !got.NameUpdatedAt.Equal(t2) {
		t.Fatalf("profile = %#v, want id %d named Ana at %v", got, sender.ID, t2)
	}
	if !got.FirstSeen.Equal(t0) || !got.LastSeen.Equal(t0) {
		t.Fatalf("contact times moved: %v..%v, want %v", got.FirstSeen, got.LastSeen, t0)
	}
	if err := db.Senders().SetDisplayName(ctx, "support", "nobody", "X", t2); err != nil {
		t.Fatalf("SetDisplayName on a missing profile: %v", err)
	}
}

func TestSenderIsScoped(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	first, _, err := db.Senders().Upsert(ctx, "a", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert a: %v", err)
	}
	second, _, err := db.Senders().Upsert(ctx, "b", "52155", "Ana", t0)
	if err != nil {
		t.Fatalf("Upsert b: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("one phone in two scopes must be two profiles")
	}
}

// TestSenderListPagesByLastSeen: the scope read is ordered by activity, the
// since filter looks at first_seen, and the cursor pages without repeating a
// row (ADR 0044).
func TestSenderListPagesByLastSeen(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	t3 := t2.Add(5 * time.Minute)
	for _, sender := range []struct {
		phone, name string
		first, last time.Time
	}{
		{"5215500000003", "Cora", t0, t0},
		{"5215500000002", "Beto", t1, t1},
		{"5215500000001", "Ana", t1, t3},
	} {
		if _, _, err := db.Senders().Upsert(ctx, "support",
			sender.phone, sender.name, sender.first); err != nil {
			t.Fatalf("Upsert %s: %v", sender.phone, err)
		}
		if !sender.last.Equal(sender.first) {
			if _, _, err := db.Senders().Upsert(ctx, "support",
				sender.phone, "", sender.last); err != nil {
				t.Fatalf("touch %s: %v", sender.phone, err)
			}
		}
	}
	if _, _, err := db.Senders().Upsert(ctx, "other", "5215599999999", "Zoe", t0); err != nil {
		t.Fatalf("Upsert other scope: %v", err)
	}

	page, err := db.Senders().List(ctx, "support", time.Time{}, 2, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Senders) != 2 ||
		page.Senders[0].Phone != "5215500000001" ||
		page.Senders[1].Phone != "5215500000002" {
		t.Fatalf("page one = %#v", page.Senders)
	}
	if page.NextCursor == "" {
		t.Fatal("a full page must carry a cursor")
	}

	next, err := db.Senders().List(ctx, "support", time.Time{}, 2, page.NextCursor)
	if err != nil {
		t.Fatalf("next page: %v", err)
	}
	if len(next.Senders) != 1 || next.Senders[0].Phone != "5215500000003" {
		t.Fatalf("page two = %#v", next.Senders)
	}
	if next.NextCursor != "" {
		t.Fatalf("last page cursor = %q, want none", next.NextCursor)
	}

	// since filters by first_seen, so the active profile created before it is
	// out even though its activity is the newest.
	since, err := db.Senders().List(ctx, "support", t1, 10, "")
	if err != nil {
		t.Fatalf("List since: %v", err)
	}
	if len(since.Senders) != 2 ||
		since.Senders[0].Phone != "5215500000001" ||
		since.Senders[1].Phone != "5215500000002" {
		t.Fatalf("since page = %#v", since.Senders)
	}

	if _, err := db.Senders().List(ctx, "support", time.Time{}, 2, "notacursor"); !errors.Is(err, botflota.ErrInvalidCursor) {
		t.Fatalf("bad cursor error = %v, want ErrInvalidCursor", err)
	}
}
