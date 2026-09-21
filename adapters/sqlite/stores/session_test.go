package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
)

func TestSessionRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	want := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if want.ID == 0 {
		t.Fatal("Save did not set a session id")
	}
	got, err := db.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSessionResumeAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")

	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	chat := ensureChat(t, first)
	want := testSession(chat.ID)
	if err := first.Sessions().Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
	got, err := second.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest after restart: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resume mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestSessionSaveUpdates(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	session := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	session.Node = "menu"
	session.VisitedNodes = []string{"menu"}
	if err := db.Sessions().Save(ctx, session); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := db.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Node != "menu" || !reflect.DeepEqual(got.VisitedNodes, []string{"menu"}) {
		t.Fatalf("update not persisted: %#v", got)
	}
}

func TestSessionRunsAreHistory(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	first := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, first); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	second := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, second); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := db.Sessions().Latest(ctx, chat.ID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.ID != second.ID || second.ID == first.ID {
		t.Fatalf("Latest = %d, want the newest run %d (older was %d)",
			got.ID, second.ID, first.ID)
	}
	if err := db.Sessions().Delete(ctx, chat.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Sessions().Latest(ctx, chat.ID); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("Latest after delete = %v, want ErrNotFound", err)
	}
}

func TestSessionNotFound(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Sessions().Latest(ctx, 999); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("Latest missing = %v, want ErrNotFound", err)
	}
}

// TestSessionCountActive covers the status metric: only unexpired runs of the
// bot count, and a run without expiry never expires.
func TestSessionCountActive(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	chat := ensureChat(t, db)
	active := testSession(chat.ID)
	if err := db.Sessions().Save(ctx, active); err != nil {
		t.Fatalf("Save active: %v", err)
	}
	never := testSession(chat.ID)
	never.ExpiresAt = time.Time{}
	if err := db.Sessions().Save(ctx, never); err != nil {
		t.Fatalf("Save never-expiring: %v", err)
	}
	expired := testSession(chat.ID)
	expired.ExpiresAt = t1.Add(-time.Hour)
	if err := db.Sessions().Save(ctx, expired); err != nil {
		t.Fatalf("Save expired: %v", err)
	}

	count, err := db.Sessions().CountActive(ctx, chat.Scope, chat.Bot, t1)
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if count != 2 {
		t.Fatalf("CountActive = %d, want 2 (expired excluded)", count)
	}
	if count, err = db.Sessions().CountActive(ctx, chat.Scope, "other", t1); err != nil {
		t.Fatalf("CountActive other: %v", err)
	} else if count != 0 {
		t.Fatalf("CountActive other = %d, want 0", count)
	}
}
