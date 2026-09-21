package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestAccessTokenRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.AccessTokens().FindByHash(ctx, []byte("missing")); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("FindByHash missing = %v, want ErrNotFound", err)
	}
	token := &botflota.AccessToken{
		Name: "dashboard", Hash: []byte{7, 8},
		Scopes: []string{"ventas", "soporte"}, CreatedAt: t0, ExpiresAt: t2,
	}
	if err := db.AccessTokens().Create(ctx, token); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if token.ID == 0 {
		t.Fatal("Create did not set an id")
	}
	got, err := db.AccessTokens().FindByHash(ctx, token.Hash)
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if got.ID != token.ID || got.Name != "dashboard" || !got.ExpiresAt.Equal(t2) {
		t.Fatalf("token = %#v", got)
	}
	if !reflect.DeepEqual(got.Scopes, []string{"soporte", "ventas"}) {
		t.Fatalf("scopes = %#v, want stable order", got.Scopes)
	}
	if !got.Active(t1) {
		t.Fatal("token should be active before expiry")
	}
	if got.Active(t2) {
		t.Fatal("token should be expired at expires_at")
	}
	tokens, err := db.AccessTokens().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tokens) != 1 || len(tokens[0].Scopes) != 2 {
		t.Fatalf("List = %#v", tokens)
	}
	if len(tokens[0].Hash) != 0 {
		t.Fatal("List must not expose hashes")
	}
	if err := db.AccessTokens().Revoke(ctx, token.ID, t1); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, err = db.AccessTokens().FindByHash(ctx, token.Hash)
	if err != nil {
		t.Fatalf("FindByHash after revoke: %v", err)
	}
	if got.Active(t1) {
		t.Fatal("revoked token still active")
	}
	if err := db.AccessTokens().Revoke(ctx, token.ID, t2); err != nil {
		t.Fatalf("Revoke again: %v", err)
	}
	if err := db.AccessTokens().Revoke(ctx, 999, t1); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Revoke unknown = %v, want ErrNotFound", err)
	}
}
