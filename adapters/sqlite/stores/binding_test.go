package stores_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

func testBinding(scope, bot, subject string) *botflota.Binding {
	return &botflota.Binding{
		Scope: scope, Bot: bot,
		Provider: "whatsapp", Adapter: "whatsmeow", Mode: "native",
		Identity: botflota.IdentityKey{Provider: "whatsapp", Subject: subject},
	}
}

func TestBindingCommitRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	if _, err := db.Bindings().Get(ctx, "support", "support-bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get missing = %v, want ErrNotFound", err)
	}
	account := testBinding("support", "support-bot", "5215500000000")
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if account.Generation != 1 {
		t.Fatalf("generation = %d, want 1", account.Generation)
	}
	if account.CreatedAt.IsZero() || account.UpdatedAt.IsZero() {
		t.Fatalf("times = %v/%v, want both set", account.CreatedAt, account.UpdatedAt)
	}
	got, err := db.Bindings().Get(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Provider != "whatsapp" || got.Adapter != "whatsmeow" ||
		got.Mode != "native" || got.Identity.Subject != "5215500000000" ||
		got.Generation != 1 || !got.CreatedAt.Equal(account.CreatedAt) {
		t.Fatalf("binding = %#v, want the committed association", got)
	}
}

// TestBindingCommitReauthKeepsGeneration: renewing the same identity space
// keeps its generation while refreshing metadata and the update time.
func TestBindingCommitReauthKeepsGeneration(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	first := testBinding("support", "support-bot", "5215500000000")
	if err := db.Bindings().Commit(ctx, first); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	second := testBinding("support", "support-bot", "5215500000000")
	second.Mode = "native"
	if err := db.Bindings().Commit(ctx, second); err != nil {
		t.Fatalf("Commit reauth: %v", err)
	}
	if second.Generation != 1 {
		t.Fatalf("generation = %d, want the renewed space to keep 1", second.Generation)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("created = %v, want the original %v", second.CreatedAt, first.CreatedAt)
	}
	if !second.UpdatedAt.After(second.CreatedAt) && !second.UpdatedAt.Equal(second.CreatedAt) {
		t.Fatalf("updated = %v, want at or after creation", second.UpdatedAt)
	}
}

// TestBindingCommitReplacementAdvancesGeneration: an unrelated identity — a
// different subject, or the same subject from another identity space —
// replaces the stored one past its generation.
func TestBindingCommitReplacementAdvancesGeneration(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	account := testBinding("support", "support-bot", "5215500000000")
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	replacement := testBinding("support", "support-bot", "5215599999999")
	if err := db.Bindings().Commit(ctx, replacement); err != nil {
		t.Fatalf("Commit replacement: %v", err)
	}
	if replacement.Generation != 2 {
		t.Fatalf("generation = %d, want 2", replacement.Generation)
	}
	got, err := db.Bindings().Get(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Identity.Subject != "5215599999999" || got.Generation != 2 {
		t.Fatalf("binding = %#v, want the replacement at generation 2", got)
	}
	spaced := testBinding("support", "support-bot", "5215599999999")
	spaced.Namespace = "profile-b"
	if err := db.Bindings().Commit(ctx, spaced); err != nil {
		t.Fatalf("Commit spaced replacement: %v", err)
	}
	if spaced.Generation != 3 {
		t.Fatalf("generation = %d, want 3", spaced.Generation)
	}
}

// TestBindingCommitRejectsIncomplete: the store records a committed
// association, never a partial row a reader must guess about.
func TestBindingCommitRejectsIncomplete(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	valid := testBinding("s", "b", "1")
	cases := []struct {
		name    string
		account *botflota.Binding
	}{
		{"no scope", &botflota.Binding{Bot: "b", Provider: "whatsapp", Identity: valid.Identity}},
		{"no bot", &botflota.Binding{Scope: "s", Provider: "whatsapp", Identity: valid.Identity}},
		{"no provider", &botflota.Binding{Scope: "s", Bot: "b", Identity: valid.Identity}},
		{"no identity", &botflota.Binding{Scope: "s", Bot: "b", Provider: "whatsapp"}},
		{
			"identity names another provider",
			&botflota.Binding{
				Scope: "s", Bot: "b", Provider: "whatsapp",
				Identity: botflota.IdentityKey{Provider: "telegram", Subject: "1"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.Bindings().Commit(ctx, tc.account); err == nil {
				t.Fatal("Commit accepted an incomplete binding")
			}
		})
	}
}

func TestLinkedAccountList(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	for _, account := range []botflota.Binding{
		{
			Scope: "z", Bot: "later", Provider: "whatsapp",
			Identity: botflota.IdentityKey{Provider: "whatsapp", Subject: "2"},
		},
		{
			Scope: "a", Bot: "first", Provider: "whatsapp",
			Identity: botflota.IdentityKey{Provider: "whatsapp", Subject: "1"},
		},
	} {
		if err := db.Bindings().Commit(ctx, &account); err != nil {
			t.Fatalf("Commit: %v", err)
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

// TestBindingDeleteKeepsHistory: detachment forgets the local binding and
// leaves chats, directory, and history alone (ADR 0053).
func TestBindingDeleteKeepsHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db := open(t, path)
	account := testBinding("support", "support-bot", "5215500000000")
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	target := botflota.ChatTarget{
		Provider: "whatsapp", Kind: botflota.ChatDirect, Thread: "5215511111111",
	}
	chat, err := db.Chats().Ensure(ctx, "support", "support-bot", target, 0, time.Now())
	if err != nil {
		t.Fatalf("Ensure chat: %v", err)
	}
	if err := db.Directory().Put(ctx, &botflota.DirectoryEntry{
		Scope: "support", Bot: "support-bot",
		PeerKey:        botflota.IdentityKey{Provider: "whatsapp", Subject: "5215522222222"},
		SourceIdentity: account.Identity,
		UpdatedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("Put directory: %v", err)
	}
	if err := db.Bindings().Delete(ctx, "support", "support-bot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Bindings().Get(ctx, "support", "support-bot"); !errors.Is(
		err, botflota.ErrNotFound,
	) {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
	if _, err := db.Chats().ForTarget(ctx, "support", "support-bot", target); err != nil {
		t.Fatalf("chat after detach = %v, want the history kept", err)
	} else {
		_ = chat
	}
	entries, err := db.Directory().List(ctx, "support", "support-bot", 0)
	if err != nil {
		t.Fatalf("List directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory = %#v, want the snapshot kept", entries)
	}
}

// TestBindingSurvivesReopen: a committed association with its generation is
// durable: closing and reopening the database keeps it (ADR 0053).
func TestBindingSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db := open(t, path)
	account := testBinding("support", "support-bot", "5215500000000")
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	replacement := testBinding("support", "support-bot", "5215599999999")
	if err := db.Bindings().Commit(ctx, replacement); err != nil {
		t.Fatalf("Commit replacement: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := open(t, path)
	got, err := reopened.Bindings().Get(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Identity.Subject != "5215599999999" || got.Generation != 2 ||
		got.Provider != "whatsapp" || got.Adapter != "whatsmeow" || got.Mode != "native" {
		t.Fatalf("binding = %#v, want the replacement at generation 2", got)
	}
}

// TestBindingGenerationsNeverRepeat: detach forgets the binding row but
// never its generation provenance, so the next association continues past
// every earlier one instead of reusing it (ADR 0053).
func TestBindingGenerationsNeverRepeat(t *testing.T) {
	ctx := context.Background()
	db := open(t, filepath.Join(t.TempDir(), "app.db"))
	first := testBinding("support", "support-bot", "5215500000000")
	if err := db.Bindings().Commit(ctx, first); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if first.Generation != 1 {
		t.Fatalf("generation = %d, want 1", first.Generation)
	}
	if err := db.Bindings().Delete(ctx, "support", "support-bot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	other := testBinding("support", "support-bot", "5215599999999")
	if err := db.Bindings().Commit(ctx, other); err != nil {
		t.Fatalf("Commit after detach: %v", err)
	}
	if other.Generation != 2 {
		t.Fatalf("generation = %d, want 2 past the detached one", other.Generation)
	}
	// Even the same identity returning after a detach advances: the
	// continuity the row carried is gone, so the store stays conservative
	// and never repeats a generation.
	if err := db.Bindings().Delete(ctx, "support", "support-bot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	back := testBinding("support", "support-bot", "5215599999999")
	if err := db.Bindings().Commit(ctx, back); err != nil {
		t.Fatalf("Commit again: %v", err)
	}
	if back.Generation != 3 {
		t.Fatalf("generation = %d, want 3", back.Generation)
	}
	got, err := db.Bindings().Get(ctx, "support", "support-bot")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Generation != 3 {
		t.Fatalf("stored generation = %d, want 3", got.Generation)
	}
}

// TestBindingGenerationsSurviveReopen: the provenance outlives a restart,
// so a replacement after a detach and a reopen still advances.
func TestBindingGenerationsSurviveReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db := open(t, path)
	account := testBinding("support", "support-bot", "5215500000000")
	if err := db.Bindings().Commit(ctx, account); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := db.Bindings().Delete(ctx, "support", "support-bot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := open(t, path)
	next := testBinding("support", "support-bot", "5215599999999")
	if err := reopened.Bindings().Commit(ctx, next); err != nil {
		t.Fatalf("Commit after reopen: %v", err)
	}
	if next.Generation != 2 {
		t.Fatalf("generation = %d, want 2", next.Generation)
	}
}
