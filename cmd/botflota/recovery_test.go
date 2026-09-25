package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfigLeavesAPendingPublicationAlone is the reason recovery moved out
// of loading: a reader that does not own the instance must never rewrite its
// files, or a command that finds the fleet running could undo the publication
// the fleet is making (ADR 0039, ADR 0042). Only ownInstance recovers.
func TestLoadConfigLeavesAPendingPublicationAlone(t *testing.T) {
	path := writeConfig(t, reloadConfig)
	journal := filepath.Join(filepath.Dir(path), ".botflota", "publish.json")
	backup := path + ".botflota-old"
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte(reloadConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	// The replaced file is invalid, so a reader that recovered would load the
	// old tree; one that leaves it alone fails instead.
	newer := strings.Replace(reloadConfig, "port: 3000", "port: 4000", 1) +
		"unknown_setting: true\n"
	if err := os.WriteFile(path, []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(map[string]any{
		"entries": []map[string]any{{"path": path, "existed": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, record, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, code := loadConfig(path); code == 0 {
		t.Fatal("loadConfig recovered a pending publication, want a refusal")
	}
	if content, err := os.ReadFile(path); err != nil ||
		!strings.Contains(string(content), "unknown_setting") {
		t.Fatalf("loadConfig rewrote the file: %s (%v)", content, err)
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatal("loadConfig cleared the journal")
	}
}

func TestLoadConfigWithoutAJournalLoadsTheFile(t *testing.T) {
	path := writeConfig(t, reloadConfig)
	cfg, code := loadConfig(path)
	if code != 0 || cfg.Port != 3000 {
		t.Fatalf("loadConfig = code %d, port %d; want 0 and 3000", code, cfg.Port)
	}
}
