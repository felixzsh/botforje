package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(existing, []byte("scopes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope.yml")
	if got, err := firstExisting([]string{missing, existing}); err != nil || got != existing {
		t.Fatalf("firstExisting = %q, %v; want the existing file", got, err)
	}
	if _, err := firstExisting([]string{missing}); err == nil ||
		!strings.Contains(err.Error(), missing) {
		t.Fatalf("want an error naming %q, got %v", missing, err)
	}
}

func TestDiscoverConfigEnvWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.yml")
	if err := os.WriteFile(path, []byte("scopes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOTFLOTA_CONFIG", path)
	found, err := discoverConfig()
	if err != nil || found != path {
		t.Fatalf("discoverConfig = %q, %v; want BOTFLOTA_CONFIG", found, err)
	}
	t.Setenv("BOTFLOTA_CONFIG", filepath.Join(t.TempDir(), "missing.yml"))
	if _, err := discoverConfig(); err == nil {
		t.Fatal("want an error for a missing BOTFLOTA_CONFIG")
	}
}

func TestDiscoverConfigUsesXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("BOTFLOTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path := filepath.Join(xdg, "botflota", "config.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("scopes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	found, err := discoverConfig()
	if err != nil || found != path {
		t.Fatalf("discoverConfig = %q, %v; want the XDG config", found, err)
	}
}
