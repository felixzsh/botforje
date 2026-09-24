package config

import (
	"path/filepath"
	"testing"
)

func TestDataDirPathRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "dev.yml", "data_dir: \"../.dev-data\"\n")
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
	path := writeTemp(t, dir, "dev.yml", "data_dir: \"/var/lib/botflota\"\n")
	cfg, err := (Loader{}).Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := cfg.DataDirPath()
	if err != nil {
		t.Fatalf("DataDirPath: %v", err)
	}
	if got != "/var/lib/botflota" {
		t.Fatalf("DataDirPath = %q, want /var/lib/botflota", got)
	}
}

func TestDataDirPathXDGDefault(t *testing.T) {
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_DATA_HOME", xdg)
	got, err := (&Config{}).DataDirPath()
	if err != nil {
		t.Fatalf("DataDirPath: %v", err)
	}
	if want := filepath.Join(xdg, "botflota"); got != want {
		t.Fatalf("DataDirPath = %q, want %q", got, want)
	}
}
