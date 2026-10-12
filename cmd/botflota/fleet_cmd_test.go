package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/felixzsh/botflota/instance"
)

// TestOwnInstance covers the guard that keeps a local write command out of a
// running fleet: ownership comes from the lock, never from the API port, and it
// is taken from the file's own data_dir before the tree is read (ADR 0039).
func TestOwnInstance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	content := "data_dir: " + strconv.Quote(dir) + "\nscopes: {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	lock, err := ownInstance(path)
	if err != nil {
		t.Fatalf("ownInstance: %v", err)
	}
	if _, err := ownInstance(path); !errors.Is(err, instance.ErrOwned) {
		t.Fatalf("second lock = %v, want instance.ErrOwned", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	lock, err = ownInstance(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// TestOwnInstanceRecoversBeforeLoading proves the ordering: a publication that
// died halfway is undone by the process that takes the instance, not by a reader
// that finds it owned.
func TestOwnInstanceRecoversBeforeLoading(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	valid := "data_dir: " + strconv.Quote(dir) + "\nscopes: {}\n"
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// A half-written tree: the file is newer and invalid, the journal and the
	// backup survived the crash.
	if err := os.WriteFile(path, []byte(valid+"unknown_setting: true\n"), 0o644); err != nil {
		t.Fatalf("write newer config: %v", err)
	}
	journal := filepath.Join(dir, ".botflota", "publish.json")
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path+".botflota-old", []byte(valid), 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	record := `{"entries":[{"path":` + strconv.Quote(path) + `,"existed":true}]}`
	if err := os.WriteFile(journal, []byte(record), 0o644); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	lock, err := ownInstance(path)
	if err != nil {
		t.Fatalf("ownInstance: %v", err)
	}
	defer lock.Release()
	cfg, code := loadConfig(path)
	if code != 0 {
		t.Fatalf("loadConfig = %d, want the recovered tree to load", code)
	}
	if cfg == nil {
		t.Fatal("no config after recovery")
	}
	if _, err := os.Stat(journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the journal survived recovery")
	}
}
