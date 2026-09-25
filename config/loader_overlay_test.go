package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles lays out a tree under a temporary directory and returns the main
// file path.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return filepath.Join(dir, "botflota.yml")
}

func TestLoaderOverlayValidatesACandidateTree(t *testing.T) {
	main := writeFiles(t, map[string]string{
		"botflota.yml": "include:\n  - extra.yml\nscopes:\n  support:\n    bots:\n      demo: {}\n",
		"extra.yml":    "scopes:\n  ventas:\n    bots:\n      other: {}\n",
	})

	t.Run("the disk tree is what the loader reads without an overlay", func(t *testing.T) {
		cfg, err := (Loader{}).Load(main)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cfg.Scopes) != 2 {
			t.Fatalf("scopes = %d, want 2", len(cfg.Scopes))
		}
	})

	t.Run("an overlay adds a file an include names but disk lacks", func(t *testing.T) {
		fragment := filepath.Join(filepath.Dir(main), "scopes", "support.yml")
		loader := Loader{Overlay: map[string][]byte{
			fragment: []byte("scopes:\n  support:\n    display_name: Support\n"),
		}}
		withInclude := strings.Replace(string(readFile(t, main)),
			"include:\n  - extra.yml\n",
			"include:\n  - extra.yml\n  - scopes/support.yml\n", 1)
		loader.Overlay[main] = []byte(withInclude)

		cfg, err := loader.Load(main)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := cfg.Scopes["support"].DisplayName; got != "Support" {
			t.Fatalf("display_name = %q, want Support from the overlay", got)
		}
		files := cfg.Files()
		if len(files) != 3 {
			t.Fatalf("files = %v, want the overlay file to be watched", files)
		}
	})

	t.Run("a directory include sees an overlay file beside the real ones", func(t *testing.T) {
		dir := t.TempDir()
		mainPath := filepath.Join(dir, "botflota.yml")
		entry := "scopes/*.yml"
		if err := os.WriteFile(mainPath,
			[]byte("include:\n  - "+entry+"\nscopes:\n  main: {}\n"), 0o644); err != nil {
			t.Fatalf("write main: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "scopes"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "scopes", "one.yml"),
			[]byte("scopes:\n  one: {}\n"), 0o644); err != nil {
			t.Fatalf("write one: %v", err)
		}
		loader := Loader{Overlay: map[string][]byte{
			filepath.Join(dir, "scopes", "two.yml"): []byte("scopes:\n  two: {}\n"),
		}}
		cfg, err := loader.Load(mainPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		for _, name := range []string{"main", "one", "two"} {
			if _, ok := cfg.Scopes[name]; !ok {
				t.Fatalf("scope %q missing: %v", name, cfg.Scopes)
			}
		}
	})

	t.Run("a glob include sees an overlay file", func(t *testing.T) {
		dir := t.TempDir()
		mainPath := filepath.Join(dir, "botflota.yml")
		if err := os.WriteFile(mainPath,
			[]byte("include:\n  - 'scopes/*.yml'\n"), 0o644); err != nil {
			t.Fatalf("write main: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "scopes"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		loader := Loader{Overlay: map[string][]byte{
			filepath.Join(dir, "scopes", "only.yml"): []byte("scopes:\n  only: {}\n"),
		}}
		cfg, err := loader.Load(mainPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if _, ok := cfg.Scopes["only"]; !ok {
			t.Fatalf("scopes = %v, want the overlay file", cfg.Scopes)
		}
	})
}

func TestLoaderOverlayLeavesDiskAlone(t *testing.T) {
	main := writeFiles(t, map[string]string{
		"botflota.yml": "scopes:\n  support:\n    bots:\n      demo: {}\n",
	})
	loader := Loader{Overlay: map[string][]byte{
		main: []byte("scopes:\n  support:\n    display_name: Support\n"),
	}}
	if _, err := loader.Load(main); err != nil {
		t.Fatalf("Load: %v", err)
	}
	content := readFile(t, main)
	if !strings.Contains(string(content), "bots") {
		t.Fatalf("the overlay reached disk: %s", content)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}
