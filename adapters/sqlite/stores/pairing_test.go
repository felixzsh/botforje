package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestPairingStatusRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Pairings().Get(ctx, "dev", "bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("pairing status missing = %v, want ErrNotFound", err)
	}
	want := &botflota.PairingStatus{
		Scope: "dev", Bot: "bot", State: botflota.PairingUnpaired, LastChange: t1,
	}
	if err := db.Pairings().Set(ctx, want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := db.Pairings().Get(ctx, "dev", "bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PairingStatus = %#v, want %#v", got, want)
	}
}
