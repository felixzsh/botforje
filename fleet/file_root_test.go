package fleet

import (
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
)

// TestBuildBotHandsItsScopeFileRootToTheChannel: the owner's root reaches the
// adapter that opens local files, and a scope without one gets none, so the
// process's own permissions apply (ADR 0047).
func TestBuildBotHandsItsScopeFileRootToTheChannel(t *testing.T) {
	cfg := loadFleetConfig(t)
	root := t.TempDir()
	cfg.FileRoots = map[string]string{"dev": root}
	seen := map[string]string{}
	store := newStore()
	if _, err := New(cfg, Options{
		Store:          store,
		Chats:          store,
		Peers:          store,
		Messages:       store,
		Turn:           store,
		Outbox:         store,
		DefaultTimeout: time.Minute,
		Now:            func() time.Time { return t0 },
		Catalog: bindCatalog(func(bind channels.Bind) (botflota.MessageChannel, error) {
			seen[bind.Bot] = bind.FileRoot
			return newFakeChannel(), nil
		}),
	}); err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := seen["alpha"]; got != root {
		t.Fatalf("alpha file root = %q, want %q", got, root)
	}
	if got, ok := seen["beta"]; !ok || got != root {
		t.Fatalf("beta file root = %q (built %v), want %q", got, ok, root)
	}
}

// bindCatalog builds a catalog whose definition exposes the whole bind to the
// test, which fakeCatalog's narrower signature hides.
func bindCatalog(
	build func(channels.Bind) (botflota.MessageChannel, error),
) *channels.Catalog {
	def := channels.Definition{
		Provider:     "whatsapp",
		Kind:         "fake",
		Modes:        []string{"native"},
		Capabilities: channels.Capabilities{Send: []botflota.MessageKind{botflota.KindText}},
		Build:        build,
	}
	catalog, err := channels.New(def)
	if err != nil {
		panic(err)
	}
	return catalog
}
