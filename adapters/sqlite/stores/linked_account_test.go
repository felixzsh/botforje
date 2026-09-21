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
	if _, err := db.LinkedAccounts().Get(ctx, "support", "support-bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	account := &botflota.LinkedAccount{
		Scope: "support", Bot: "support-bot", JID: "5215500000000@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatalf("Put: %v", err)
	}
	account.JID = "5215599999999@s.whatsapp.net"
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatalf("Put update: %v", err)
	}
	got, err := db.LinkedAccounts().Get(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.JID != "5215599999999@s.whatsapp.net" {
		t.Fatalf("JID = %q", got.JID)
	}
	if err := db.LinkedAccounts().Delete(ctx, "support", "support-bot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.LinkedAccounts().Get(ctx, "support", "support-bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

func TestLinkedAccountList(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, account := range []botflota.LinkedAccount{
		{Scope: "z", Bot: "later", JID: "2@s.whatsapp.net"},
		{Scope: "a", Bot: "first", JID: "1@s.whatsapp.net"},
	} {
		if err := db.LinkedAccounts().Put(ctx, &account); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	got, err := db.LinkedAccounts().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Bot != "first" || got[1].Bot != "later" {
		t.Fatalf("List = %#v, want stable scope/bot order", got)
	}
}
