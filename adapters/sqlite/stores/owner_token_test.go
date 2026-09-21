package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestOwnerTokenRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.OwnerTokens().Get(ctx); !errors.Is(err, botflota.ErrNotFound) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	want := &botflota.OwnerToken{Hash: []byte{1, 2, 3}, CreatedAt: t0}
	if err := db.OwnerTokens().Put(ctx, want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := db.OwnerTokens().Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OwnerToken = %#v, want %#v", got, want)
	}
	rotated := &botflota.OwnerToken{Hash: []byte{9}, CreatedAt: t1}
	if err := db.OwnerTokens().Put(ctx, rotated); err != nil {
		t.Fatalf("Put rotate: %v", err)
	}
	got, err = db.OwnerTokens().Get(ctx)
	if err != nil {
		t.Fatalf("Get after rotate: %v", err)
	}
	if !reflect.DeepEqual(got, rotated) {
		t.Fatalf("OwnerToken after rotate = %#v, want %#v", got, rotated)
	}
}
