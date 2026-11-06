package fleet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
)

// catalogFleetConfig is one scope with a model key resolved from the owner's
// catalog, on the fake channel the fleet tests install.
func catalogFleetConfig(catalog string) string {
	var declared []string
	if catalog != "" {
		declared = []string{"secret_files:", fmt.Sprintf("  dev: %q", catalog)}
	}
	return strings.Join(append(declared, strings.Split(strings.Join([]string{
		"scopes:",
		"  dev:",
		"    llms:",
		"      primary:",
		"        provider: openai",
		"        model: gpt-4o-mini",
		"        api_key: ${API_KEY}",
		"    actions:",
		"      greet:",
		"        steps:",
		"          - message:",
		"              body: \"hello from {{bot.id}}\"",
		"    graphs:",
		"      menu:",
		"        root: \"menu\"",
		"        nodes:",
		"          menu:",
		"            on_enter: [\"greet\"]",
		"            edges: []",
		"    bots:",
		"      alpha:",
		"        graph: \"menu\"",
		"        channel:",
		"          provider: whatsapp",
		"          mode: native",
		"",
	}, "\n"), "\n")...), "\n")
}

// writeCatalogFleet lays down a main file and the catalog it declares, and
// returns the main path and the catalog path.
func writeCatalogFleet(t *testing.T, key string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	catalog := filepath.Join(dir, "catalog.env")
	if err := os.WriteFile(catalog, []byte("API_KEY="+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(main, []byte(catalogFleetConfig(catalog)), 0o644); err != nil {
		t.Fatal(err)
	}
	return main, catalog
}

func loadCatalogFleet(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := (config.Loader{}).Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

// newCatalogRunner builds a fleet from cfg without starting it, so a test can
// drive the live-apply path directly.
func newCatalogRunner(t *testing.T, cfg *config.Config) *Fleet {
	t.Helper()
	store := newStore()
	runner, err := New(cfg, Options{
		Store: store, Chats: store, Peers: store, Messages: store,
		Turn: store, Outbox: store, DefaultTimeout: time.Minute,
		Now: func() time.Time { return t0 },
		Catalog: fakeCatalog(func(string, string, any) (botflota.MessageChannel, error) {
			return newFakeChannel(), nil
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

// TestApplyConfigHoldsBackAChangedSecretCatalog proves a catalog change never
// reaches a running fleet: a rotation and a removed declaration both stay
// pending, and the active scopes keep the values they already hold (ADR 0049).
func TestApplyConfigHoldsBackAChangedSecretCatalog(t *testing.T) {
	t.Run("a rotated catalog stays pending", func(t *testing.T) {
		main, catalog := writeCatalogFleet(t, "sk-one")
		current := loadCatalogFleet(t, main)
		runner := newCatalogRunner(t, current)
		source := config.NewSource(current)

		if err := os.WriteFile(catalog, []byte("API_KEY=sk-two\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		candidate := loadCatalogFleet(t, main)
		if got := catalogKey(candidate); got != "sk-two" {
			t.Fatalf("the candidate resolved %q, want the rotated value", got)
		}

		plan := config.PlanReload(current, candidate)
		applied, pending, err := runner.ApplyConfig(
			context.Background(), candidate, plan.Scopes, source)
		if err != nil {
			t.Fatalf("ApplyConfig: %v", err)
		}
		if len(applied) != 0 {
			t.Fatalf("applied = %v, want nothing live", applied)
		}
		if len(pending["dev"]) == 0 {
			t.Fatal("dev is not pending, so the rotation would go live")
		}
		if got := catalogKey(source.Current()); got != "sk-one" {
			t.Fatalf("the active scope resolved %q, want the value it already held", got)
		}
		runner.mu.Lock()
		active := catalogKey(runner.cfg)
		runner.mu.Unlock()
		if active != "sk-one" {
			t.Fatalf("the fleet config resolved %q, want the value it already held", active)
		}
	})

	t.Run("a removed declaration stays pending", func(t *testing.T) {
		main, _ := writeCatalogFleet(t, "sk-one")
		current := loadCatalogFleet(t, main)
		runner := newCatalogRunner(t, current)
		source := config.NewSource(current)

		t.Setenv("API_KEY", "sk-environment")
		if err := os.WriteFile(main, []byte(catalogFleetConfig("")), 0o644); err != nil {
			t.Fatal(err)
		}
		candidate, err := (config.Loader{}).Load(main)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got := catalogKey(candidate); got != "sk-environment" {
			t.Fatalf("the candidate resolved %q, want the environment", got)
		}
		// With no catalog declared, the scope's document is unchanged, so the
		// plan must still hold it back for the restart the owner has not made.
		plan := config.PlanReload(current, candidate)
		if len(plan.ScopeReasons["dev"]) == 0 {
			t.Fatalf("plan = %+v, want dev pending for a restart", plan.ScopeReasons)
		}
		applied, _, err := runner.ApplyConfig(
			context.Background(), candidate, plan.Scopes, source)
		if err != nil {
			t.Fatalf("ApplyConfig: %v", err)
		}
		if len(applied) != 0 {
			t.Fatalf("applied = %v, want nothing live", applied)
		}
		if got := catalogKey(source.Current()); got != "sk-one" {
			t.Fatalf("the active scope resolved %q, want the catalog value", got)
		}
	})
}

func catalogKey(cfg *config.Config) string {
	return cfg.Scopes["dev"].LLMs["primary"].APIKey.Reveal()
}
