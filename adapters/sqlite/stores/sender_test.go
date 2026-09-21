package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

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
