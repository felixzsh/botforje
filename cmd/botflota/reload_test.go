package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/felixzsh/botflota/config"
)

const reloadConfig = `port: 3000
scopes:
  support:
    graphs:
      menu:
        root: "a"
        nodes:
          a:
            edges: []
    bots:
      demo:
        graph: "menu"
        channel:
          provider: whatsapp
          mode: native
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustLoad(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := (config.Loader{}).Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestReloadOutcomeAppliesAndListsItsFiles(t *testing.T) {
	path := writeConfig(t, reloadConfig)
	next, reasons, err := reloadOutcome(path, mustLoad(t, path), testCatalog(t))
	if err != nil || len(reasons) > 0 {
		t.Fatalf("outcome = %v, %v, want an applied config", reasons, err)
	}
	if next == nil {
		t.Fatal("next = nil")
	}
	if !slices.Contains(next.Files(), path) {
		t.Fatalf("files = %v, want the main file", next.Files())
	}
}

func TestReloadOutcomeKeepsCurrentOnInvalidConfig(t *testing.T) {
	path := writeConfig(t, reloadConfig)
	current := mustLoad(t, path)
	invalid := strings.Replace(reloadConfig, `graph: "menu"`,
		`graph: "ghost"`, 1)
	if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	next, reasons, err := reloadOutcome(path, current, testCatalog(t))
	if err == nil {
		t.Fatal("an unknown graph must fail validation")
	}
	if next != nil || len(reasons) > 0 {
		t.Fatalf("outcome = %v, %v, want nothing applied", next, reasons)
	}
}

func TestReloadOutcomeReportsRestartRequired(t *testing.T) {
	path := writeConfig(t, reloadConfig)
	current := mustLoad(t, path)
	restarted := strings.Replace(reloadConfig, "port: 3000", "port: 4000", 1)
	if err := os.WriteFile(path, []byte(restarted), 0o644); err != nil {
		t.Fatal(err)
	}
	next, reasons, err := reloadOutcome(path, current, testCatalog(t))
	if err != nil {
		t.Fatalf("outcome: %v", err)
	}
	if next != nil {
		t.Fatal("a restart-required config must not be applied")
	}
	if !slices.Contains(reasons, "global setting port") {
		t.Fatalf("reasons = %v, want the global setting", reasons)
	}
}

func TestReloadOutcomeAppliesASafeChange(t *testing.T) {
	path := writeConfig(t, reloadConfig)
	current := mustLoad(t, path)
	safe := strings.Replace(reloadConfig, `          a:
            edges: []`, `          a:
            edges:
              - intent: "next"
                goto: "b"
          b:
            edges: []`, 1)
	if safe == reloadConfig {
		t.Fatal("fixture did not change")
	}
	if err := os.WriteFile(path, []byte(safe), 0o644); err != nil {
		t.Fatal(err)
	}
	next, reasons, err := reloadOutcome(path, current, testCatalog(t))
	if err != nil {
		t.Fatalf("outcome: %v", err)
	}
	if len(reasons) > 0 {
		t.Fatalf("reasons = %v, want a live change", reasons)
	}
	if next == nil {
		t.Fatal("next = nil")
	}
}

func TestFilesStampTracksNewIncludeFiles(t *testing.T) {
	dir := t.TempDir()
	before := filesStamp([]string{dir})
	if err := os.WriteFile(filepath.Join(dir, "new-scope.yml"),
		[]byte("scopes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if after := filesStamp([]string{dir}); after == before {
		t.Fatal("directory stamp did not change after adding an include file")
	}
}
