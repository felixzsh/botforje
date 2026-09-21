package whatsapp

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestValidateAccountOwners(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	accounts := db.LinkedAccounts()
	for _, account := range []botflota.LinkedAccount{
		{Scope: "dev", Bot: "first", JID: "5215500000000:1@s.whatsapp.net"},
		{Scope: "dev", Bot: "second", JID: "5215500000000:2@s.whatsapp.net"},
	} {
		if err := accounts.Put(ctx, &account); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateAccountOwners(ctx, accounts); err == nil {
		t.Fatal("duplicate phone number was accepted")
	}
}

func TestValidateAccountsAllowsMissingDevice(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAccounts(ctx, devices, db.LinkedAccounts(), db.Pairings()); err != nil {
		t.Fatalf("ValidateAccounts: %v", err)
	}
	status, err := db.Pairings().Get(ctx, account.Scope, account.Bot)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != botflota.PairingUnpaired {
		t.Fatalf("state = %q, want unpaired", status.State)
	}
}

func TestCheckPairingAllowsRecoverableStates(t *testing.T) {
	cases := []struct {
		name    string
		account *botflota.LinkedAccount
		want    botflota.PairingState
	}{
		{name: "not paired", want: botflota.PairingNotPaired},
		{
			name: "unpaired",
			account: &botflota.LinkedAccount{
				Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
			},
			want: botflota.PairingUnpaired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := openWhatsAppTestDB(t)
			if tc.account != nil {
				if err := db.LinkedAccounts().Put(ctx, tc.account); err != nil {
					t.Fatal(err)
				}
			}
			devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
			if err != nil {
				t.Fatal(err)
			}
			state, err := CheckPairing(ctx, devices, db.LinkedAccounts(),
				db.Pairings(), "dev", "botsito")
			if err != nil {
				t.Fatalf("CheckPairing: %v", err)
			}
			if state != tc.want {
				t.Fatalf("state = %q, want %q", state, tc.want)
			}
		})
	}
}

func TestUnpairRemovesOrphanedAccount(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.LinkedAccount{
		Scope: "dev", Bot: "botsito", JID: "5215500000000:1@s.whatsapp.net",
	}
	if err := db.LinkedAccounts().Put(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := Unpair(ctx, UnpairOptions{
		Scope: account.Scope, Bot: account.Bot, Devices: devices,
		Accounts: db.LinkedAccounts(), Pairings: db.Pairings(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LinkedAccounts().Get(ctx, account.Scope, account.Bot); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("account after unpair = %v, want not found", err)
	}
}

func TestPairingStatusChangesOnlyOnStateChange(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	pairings := db.Pairings()
	if err := UpdatePairingStatus(ctx, pairings, "dev", "bot",
		botflota.PairingUnpaired, t0); err != nil {
		t.Fatal(err)
	}
	if err := UpdatePairingStatus(ctx, pairings, "dev", "bot",
		botflota.PairingUnpaired, t1); err != nil {
		t.Fatal(err)
	}
	status, err := pairings.Get(ctx, "dev", "bot")
	if err != nil {
		t.Fatal(err)
	}
	if !status.LastChange.Equal(t0) {
		t.Fatalf("same state changed timestamp to %s, want %s", status.LastChange, t0)
	}
	if err := UpdatePairingStatus(ctx, pairings, "dev", "bot",
		botflota.PairingPaired, t1); err != nil {
		t.Fatal(err)
	}
	status, err = pairings.Get(ctx, "dev", "bot")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != botflota.PairingPaired || !status.LastChange.Equal(t1) {
		t.Fatalf("status = %#v, want paired at %s", status, t1)
	}
}
