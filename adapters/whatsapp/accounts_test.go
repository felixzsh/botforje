package whatsapp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

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
		Outbox: db.Outbox(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LinkedAccounts().Get(ctx, account.Scope, account.Bot); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("account after unpair = %v, want not found", err)
	}
}

// fakeOutbox records the FailPending the unlink makes.
type fakeOutbox struct {
	calls      int
	scope, bot string
	reason     string
}

func (f *fakeOutbox) NextPending(
	context.Context, string, string, int,
) ([]botflota.OutboxEntry, error) {
	return nil, nil
}

func (f *fakeOutbox) MarkSent(
	context.Context, int64, string, time.Time,
) error {
	return nil
}

func (f *fakeOutbox) MarkRetry(
	context.Context, int64, string, time.Time, time.Time,
) error {
	return nil
}

func (f *fakeOutbox) MarkFailed(
	context.Context, int64, string, time.Time,
) error {
	return nil
}

func (f *fakeOutbox) Depth(context.Context, string, string) (int, error) {
	return 0, nil
}

func (f *fakeOutbox) FailPending(
	_ context.Context, scope, bot, reason string, _ time.Time,
) (int, error) {
	f.calls++
	f.scope, f.bot, f.reason = scope, bot, reason
	return 1, nil
}

func (f *fakeOutbox) FailPendingForChat(
	context.Context, int64, string, time.Time,
) (int, error) {
	return 0, nil
}

func (f *fakeOutbox) IsPending(context.Context, int64) (bool, error) {
	return true, nil
}

// TestUnpairFailsTheQueuedReplies: unlinking ends what the link still owes
// in the same operation, so a later pairing never sends those replies from
// a different account (ADR 0040).
func TestUnpairFailsTheQueuedReplies(t *testing.T) {
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
	outbox := &fakeOutbox{}
	if err := Unpair(ctx, UnpairOptions{
		Scope: account.Scope, Bot: account.Bot, Devices: devices,
		Accounts: db.LinkedAccounts(), Pairings: db.Pairings(),
		Outbox: outbox,
	}); err != nil {
		t.Fatal(err)
	}
	if outbox.calls != 1 || outbox.scope != "dev" || outbox.bot != "botsito" ||
		outbox.reason != unlinkedReason {
		t.Fatalf("fail pending = %d %s/%s %q, want the unlinked replies",
			outbox.calls, outbox.scope, outbox.bot, outbox.reason)
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
func TestNumber(t *testing.T) {
	cases := map[string]string{
		"5215500000000:65@s.whatsapp.net": "5215500000000",
		"5215500000000@s.whatsapp.net":    "5215500000000",
		"5215500000000:65":                "5215500000000",
		"5215500000000":                   "5215500000000",
	}
	for in, want := range cases {
		if got := Number(in); got != want {
			t.Fatalf("Number(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestOwnershipConflictNamesNoOtherBot holds the public text of the one-phone
// rule: the conflict may live in a scope the caller cannot see, so a pairing
// session reports only the reason and the detail stays in the instance log
// (ADR 0021).
func TestOwnershipConflictNamesNoOtherBot(t *testing.T) {
	err := duplicateAccountError("5215500000000",
		botflota.LinkedAccount{
			Scope: "hidden", Bot: "peer", JID: "5215500000000@s.whatsapp.net",
		},
		botflota.LinkedAccount{Scope: "support", Bot: "demo"})
	if !errors.Is(err, botflota.ErrAccountInUse) {
		t.Fatalf("error = %v, want ErrAccountInUse", err)
	}
	text := pairingErrorText(err)
	for _, leak := range []string{"hidden", "peer", "5215500000000"} {
		if strings.Contains(text, leak) {
			t.Fatalf("session error = %q, want no name from another scope", text)
		}
	}
	if text == "" {
		t.Fatal("the session reports no reason")
	}
}
