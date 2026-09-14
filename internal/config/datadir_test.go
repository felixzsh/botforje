package config

import (
	"path/filepath"
	"testing"
)

func TestDataDirPathRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "dev.yml", "version: 1\ndata_dir: \"../.dev-data\"\n")
	cfg, err := (Loader{}).Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := cfg.DataDirPath()
	if err != nil {
		t.Fatalf("DataDirPath: %v", err)
	}
	if want := filepath.Join(dir, "..", ".dev-data"); got != want {
		t.Fatalf("DataDirPath = %q, want %q", got, want)
	}
}

func TestDataDirPathAbsolute(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "dev.yml", "version: 1\ndata_dir: \"/var/lib/botforje\"\n")
	cfg, err := (Loader{}).Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := cfg.DataDirPath()
	if err != nil {
		t.Fatalf("DataDirPath: %v", err)
	}
	if got != "/var/lib/botforje" {
		t.Fatalf("DataDirPath = %q, want /var/lib/botforje", got)
	}
}

func TestDataDirPathXDGDefault(t *testing.T) {
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_DATA_HOME", xdg)
	got, err := (&Config{}).DataDirPath()
	if err != nil {
		t.Fatalf("DataDirPath: %v", err)
	}
	if want := filepath.Join(xdg, "botforje"); got != want {
		t.Fatalf("DataDirPath = %q, want %q", got, want)
	}
}
