package fleet

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
)

// bindRecorder collects the file root each bind carried, keyed by
// "scope/bot", so a test can tell a per-scope lookup from a hardcoded value
// and from an empty, never-populated one.
type bindRecorder struct {
	mu      sync.Mutex
	build   map[string]string
	prepare map[string]string
	setup   map[string]string
}

func newBindRecorder() *bindRecorder {
	return &bindRecorder{
		build:   map[string]string{},
		prepare: map[string]string{},
		setup:   map[string]string{},
	}
}

func (r *bindRecorder) record(target map[string]string) func(channels.Bind) {
	return func(bind channels.Bind) {
		r.mu.Lock()
		defer r.mu.Unlock()
		target[bind.Scope+"/"+bind.Bot] = bind.FileRoot
	}
}

func (r *bindRecorder) catalog() *channels.Catalog {
	def := channels.Definition{
		Provider:     "whatsapp",
		Kind:         "fake",
		Modes:        []string{"native"},
		Capabilities: channels.Capabilities{Send: []botflota.MessageKind{botflota.KindText}},
		Build: func(bind channels.Bind) (botflota.MessageChannel, error) {
			r.record(r.build)(bind)
			return newFakeChannel(), nil
		},
		Prepare: func(_ context.Context, bind channels.Bind) (bool, string, error) {
			r.record(r.prepare)(bind)
			return true, "", nil
		},
		Setup: func(bind channels.Bind) (
			botflota.AccountLifecycle, botflota.SetupRunner, error,
		) {
			r.record(r.setup)(bind)
			return nil, nil, nil
		},
	}
	catalog, err := channels.New(def)
	if err != nil {
		panic(err)
	}
	return catalog
}

// TestEveryBindCarriesItsScopeRoot: build, prepare, and setup all receive the
// root of the scope they serve, and a scope that declares none receives an
// empty root deliberately, so a reader in a prepare or setup hook is confined
// like one in a send path (ADR 0047).
func TestEveryBindCarriesItsScopeRoot(t *testing.T) {
	cfg := loadFleetConfig(t)
	dev := cfg.Scopes["dev"]
	cfg.Scopes["ops"] = dev
	cfg.Scopes["bare"] = dev
	cfg.FileRoots = map[string]string{"dev": t.TempDir(), "ops": t.TempDir()}
	want := map[string]string{
		"dev/alpha":  cfg.FileRoots["dev"],
		"dev/beta":   cfg.FileRoots["dev"],
		"ops/alpha":  cfg.FileRoots["ops"],
		"ops/beta":   cfg.FileRoots["ops"],
		"bare/alpha": "",
		"bare/beta":  "",
	}
	recorder := newBindRecorder()
	store := newStore()
	runner, err := New(cfg, Options{
		Store:          store,
		Chats:          store,
		Peers:          store,
		Messages:       store,
		Turn:           store,
		Outbox:         store,
		DefaultTimeout: time.Minute,
		Now:            func() time.Time { return t0 },
		Catalog:        recorder.catalog(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, scope := range []string{"dev", "ops", "bare"} {
		for _, bot := range []string{"alpha", "beta"} {
			if _, _, err := runner.SetupFor(scope, bot); err != nil {
				t.Fatalf("SetupFor(%s, %s): %v", scope, bot, err)
			}
		}
	}

	for _, stage := range []struct {
		name     string
		recorded map[string]string
	}{
		{"build", recorder.build},
		{"prepare", recorder.prepare},
		{"setup", recorder.setup},
	} {
		for ref, root := range want {
			got, ok := stage.recorded[ref]
			if !ok {
				t.Errorf("%s never saw %s", stage.name, ref)
				continue
			}
			if got != root {
				t.Errorf("%s of %s got file root %q, want %q",
					stage.name, ref, got, root)
			}
		}
	}
}
