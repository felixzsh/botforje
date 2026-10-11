package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestLinkedAccountRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Bindings().Get(ctx, "support", "support-bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	account := &botflota.Binding{
		Scope: "support", Bot: "support-bot",
		Identity: waKey("5215500000000"),
	}
	if err := db.Bindings().Put(ctx, account); err != nil {
		t.Fatalf("Put: %v", err)
	}
	account.Identity.Subject = "5215599999999"
	if err := db.Bindings().Put(ctx, account); err != nil {
		t.Fatalf("Put update: %v", err)
	}
	got, err := db.Bindings().Get(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Identity.Subject != "5215599999999" ||
		got.Identity.Provider != "whatsapp" {
		t.Fatalf("identity = %#v", got.Identity)
	}
	if err := db.Bindings().Delete(ctx, "support", "support-bot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Bindings().Get(ctx, "support", "support-bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestLinkedAccountList(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, account := range []botflota.Binding{
		{Scope: "z", Bot: "later", Identity: botflota.IdentityKey{Provider: "whatsapp", Subject: "2"}},
		{Scope: "a", Bot: "first", Identity: botflota.IdentityKey{Provider: "whatsapp", Subject: "1"}},
	} {
		if err := db.Bindings().Put(ctx, &account); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	got, err := db.Bindings().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Bot != "first" || got[1].Bot != "later" {
		t.Fatalf("List = %#v, want stable scope/bot order", got)
	}
}
