package whatsapp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

func TestValidateAccountOwners(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	accounts := db.Bindings()
	for _, account := range []botflota.Binding{
		{Scope: "dev", Bot: "first", Provider: "whatsapp", Identity: waKey("5215500000000")},
		{Scope: "dev", Bot: "second", Provider: "whatsapp", Identity: waKey("5215500000000")},
	} {
		if err := accounts.Commit(ctx, &account); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateAccountOwners(ctx, accounts); err == nil {
		t.Fatal("duplicate phone number was accepted")
	}
}

func TestUnpairRemovesOrphanedAccount(t *testing.T) {
	ctx := context.Background()
	db := openWhatsAppTestDB(t)
	account := &botflota.Binding{
		Scope: "dev", Bot: "botsito", Provider: "whatsapp", Identity: waKey("5215500000000"),
	}
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	if err := Unpair(ctx, UnpairOptions{
		Scope: account.Scope, Bot: account.Bot, Devices: devices,
		Bindings: db.Bindings(), Outbox: db.Outbox(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bindings().Get(ctx, account.Scope, account.Bot); !errors.Is(
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

func (f *fakeOutbox) FailPendingForTarget(
	context.Context, string, string, botflota.ChatTarget, string, time.Time,
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
	account := &botflota.Binding{
		Scope: "dev", Bot: "botsito", Provider: "whatsapp", Identity: waKey("5215500000000"),
	}
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatal(err)
	}
	devices, err := OpenDevices(ctx, db.DB, nil, slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	outbox := &fakeOutbox{}
	if err := Unpair(ctx, UnpairOptions{
		Scope: account.Scope, Bot: account.Bot, Devices: devices,
		Bindings: db.Bindings(), Outbox: outbox,
	}); err != nil {
		t.Fatal(err)
	}
	if outbox.calls != 1 || outbox.scope != "dev" || outbox.bot != "botsito" ||
		outbox.reason != unlinkedReason {
		t.Fatalf("fail pending = %d %s/%s %q, want the unlinked replies",
			outbox.calls, outbox.scope, outbox.bot, outbox.reason)
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

// TestOwnershipConflictNamesNoOtherBot holds the sanitized public text of the
// one-phone rule: the conflict may live in a scope the caller cannot see, so a
// setup session reports only a sanitized reason and the detail stays in the
// instance log (ADR 0021).
func TestOwnershipConflictNamesNoOtherBot(t *testing.T) {
	err := duplicateAccountError(ingest.PeerKey("5215500000000"),
		botflota.Binding{
			Scope: "hidden", Bot: "peer", Provider: "whatsapp", Identity: waKey("5215500000000"),
		},
		botflota.Binding{Scope: "support", Bot: "demo"})
	if !errors.Is(err, botflota.ErrIdentityInUse) {
		t.Fatalf("error = %v, want ErrAccountInUse", err)
	}
	code := setupErrorCode(err)
	if code != "identity_in_use" {
		t.Fatalf("setup error code = %q, want the sanitized reason", code)
	}
	for _, leak := range []string{"hidden", "peer", "5215500000000"} {
		if strings.Contains(code, leak) {
			t.Fatalf("session error = %q, want no name from another scope", code)
		}
	}
}
