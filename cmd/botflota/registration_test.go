package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// fakeAdapter is one registered adapter for the assembly tests: a declaration
// with no transport, plus hooks that record what the command layer did with it.
type fakeAdapter struct {
	provider   string
	mode       string
	installErr error
	validate   error
	setup      *stubOfflineRunner

	opened    int
	validated int
	closed    int
}

func (f *fakeAdapter) installation() channels.Installation {
	provider, mode := f.provider, f.mode
	if provider == "" {
		provider, mode = "fake", "test"
	}
	def := channels.Definition{
		Provider: provider,
		Kind:     "in-memory",
		Modes:    []string{mode},
		Decode:   func(spec.Bot) (any, []string) { return nil, nil },
	}
	return channels.Installation{
		Definition: def,
		Install: func(_ context.Context, host channels.Host) (channels.Installed, error) {
			f.opened++
			if host.DB == nil || host.Bindings == nil || host.Bot == nil {
				return channels.Installed{}, errors.New("the host is incomplete")
			}
			if f.installErr != nil {
				return channels.Installed{}, f.installErr
			}
			bound := def
			bound.Build = func(channels.Bind) (botflota.MessageChannel, error) {
				return nil, errors.New("the fake builds no channel")
			}
			if f.setup != nil {
				runner := f.setup
				bound.Setup = func(channels.Bind) (
					botflota.AccountLifecycle, botflota.SetupRunner, error,
				) {
					return &stubOfflineLifecycle{}, runner, nil
				}
			}
			return channels.Installed{
				Definition: bound,
				Validate: func(context.Context) error {
					f.validated++
					return f.validate
				},
				Close: func() { f.closed++ },
			}, nil
		},
	}
}

// configWithBots is a config document that runs one bot per selector.
func configWithBots(t *testing.T, selectors ...spec.Channel) *config.Config {
	t.Helper()
	bots := make(map[string]spec.Bot, len(selectors))
	for i, selector := range selectors {
		bots["bot"+strconv.Itoa(i)] = spec.Bot{Graph: "main", Channel: selector}
	}
	return &config.Config{Scopes: map[string]spec.Scope{"dev": {Bots: bots}}}
}

// testDB opens one instance database for an assembly test.
func testDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "botflota.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestPureCatalogCarriesNoRuntimeHooks proves config-only validation runs on
// the installed declarations alone: no registry entry opens anything there
// (ADR 0052, ADR 0054).
func TestPureCatalogCarriesNoRuntimeHooks(t *testing.T) {
	catalog, err := pureCatalog()
	if err != nil {
		t.Fatalf("pureCatalog: %v", err)
	}
	defs := catalog.Definitions()
	if len(defs) == 0 {
		t.Fatal("no adapter is installed")
	}
	for _, def := range defs {
		if def.Build != nil || def.Prepare != nil || def.Setup != nil {
			t.Fatalf("provider %q: the pure catalog carries a runtime hook",
				def.Provider)
		}
	}
}

// TestChannelRuntimeInstallsOnlyTheSelectedAdapter proves the lazy assembly:
// the selected entry opens with the neutral host, the unselected one stays as
// its pure declaration, and Close runs once (ADR 0052, ADR 0053, ADR 0054).
func TestChannelRuntimeInstallsOnlyTheSelectedAdapter(t *testing.T) {
	db := testDB(t)
	chosen := &fakeAdapter{provider: "fake", mode: "test"}
	other := &fakeAdapter{provider: "other", mode: "remote"}
	cfg := configWithBots(t, spec.Channel{Provider: "fake", Mode: "test"})
	catalog, runtimes, err := channelRuntime(context.Background(), cfg, db,
		botflota.DiscardLogger(), configBotLookup(cfg), offlineStart,
		[]channels.Installation{chosen.installation(), other.installation()})
	if err != nil {
		t.Fatalf("channelRuntime: %v", err)
	}
	defer runtimes.Close()

	if chosen.opened != 1 {
		t.Fatalf("selected installs = %d, want 1", chosen.opened)
	}
	if other.opened != 0 {
		t.Fatalf("unselected installs = %d, want 0", other.opened)
	}
	if err := runtimes.validate(context.Background()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if chosen.validated != 1 {
		t.Fatalf("selected validations = %d, want 1", chosen.validated)
	}
	def, _, err := catalog.Resolve(spec.Channel{Provider: "fake", Mode: "test"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if def.Build == nil {
		t.Fatal("the selected definition is not bound")
	}
	spare, _, err := catalog.Resolve(spec.Channel{Provider: "other", Mode: "remote"})
	if err != nil {
		t.Fatalf("Resolve unselected: %v", err)
	}
	if spare.Build != nil {
		t.Fatal("the unselected definition was opened")
	}

	runtimes.Close()
	if chosen.closed != 1 {
		t.Fatalf("close = %d, want 1", chosen.closed)
	}
	runtimes.Close()
	if chosen.closed != 1 {
		t.Fatalf("second close = %d, want 1", chosen.closed)
	}
}

// TestChannelRuntimeClosesWhatItOpenedOnError proves an install that fails
// after another adapter opened releases that adapter's resources.
func TestChannelRuntimeClosesWhatItOpenedOnError(t *testing.T) {
	db := testDB(t)
	first := &fakeAdapter{provider: "fake", mode: "test"}
	second := &fakeAdapter{
		provider: "other", mode: "remote", installErr: errors.New("boom"),
	}
	cfg := configWithBots(t,
		spec.Channel{Provider: "fake", Mode: "test"},
		spec.Channel{Provider: "other", Mode: "remote"},
	)
	_, _, err := channelRuntime(context.Background(), cfg, db,
		botflota.DiscardLogger(), configBotLookup(cfg), offlineStart,
		[]channels.Installation{first.installation(), second.installation()})
	if err == nil {
		t.Fatal("a failed install was reported as success")
	}
	if first.closed != 1 {
		t.Fatalf("close after a failed install = %d, want 1", first.closed)
	}
}

// TestChannelRuntimeReportsDurableState proves the adapter's own durable-state
// check reaches the caller (ADR 0021, ADR 0053).
func TestChannelRuntimeReportsDurableState(t *testing.T) {
	db := testDB(t)
	adapter := &fakeAdapter{validate: errors.New("duplicate account")}
	cfg := configWithBots(t, spec.Channel{Provider: "fake", Mode: "test"})
	_, runtimes, err := channelRuntime(context.Background(), cfg, db,
		botflota.DiscardLogger(), configBotLookup(cfg), offlineStart,
		[]channels.Installation{adapter.installation()})
	if err != nil {
		t.Fatalf("channelRuntime: %v", err)
	}
	defer runtimes.Close()
	if err := runtimes.validate(context.Background()); err == nil {
		t.Fatal("the adapter's durable-state check was not reported")
	}
}
