package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfigRecoversAnInterruptedPublication proves the startup path: a
// process that died between replacing two files leaves a journal behind, and the
// next start must land on the last complete tree rather than the half-written
// one. The newer main file here is invalid on purpose, so without recovery the
// startup would refuse instead of quietly loading the old tree.
func TestLoadConfigRecoversAnInterruptedPublication(t *testing.T) {
	path := writeConfig(t, reloadConfig)
	journal := filepath.Join(filepath.Dir(path), ".botflota", "publish.json")
	backup := path + ".botflota-old"

	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte(reloadConfig), 0o644); err != nil {
		t.Fatal(err)
	}
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

	cfg, code := loadConfig(path)
	if code != 0 {
		t.Fatalf("loadConfig = code %d, want the recovered tree to load", code)
	}
	if cfg.Port != 3000 {
		t.Fatalf("port = %d, want the recovered 3000", cfg.Port)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "unknown_setting") {
		t.Fatal("the half-written file survived recovery")
	}
	for _, leftover := range []string{journal, backup} {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Fatalf("%s survived recovery", filepath.Base(leftover))
		}
	}
}

func TestLoadConfigWithoutAJournalLoadsTheFile(t *testing.T) {
	path := writeConfig(t, reloadConfig)
	cfg, code := loadConfig(path)
	if code != 0 || cfg.Port != 3000 {
		t.Fatalf("loadConfig = code %d, port %d; want 0 and 3000", code, cfg.Port)
	}
}
