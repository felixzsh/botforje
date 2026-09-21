package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixzsh/botflota/config"
)

func TestInitWritesConfigAndSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := initConfig(path, false); err != nil {
		t.Fatalf("initConfig: %v", err)
	}
	cfg, err := (config.Loader{}).Load(path)
	if err != nil {
		t.Fatalf("load the generated config: %v", err)
	}
	if errs := config.Validate(cfg); len(errs) != 0 {
		t.Fatalf("the generated config does not validate: %v", errs)
	}
	configData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the generated config: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, schemaFileName))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	// Both files are user-facing: they must read as documentation, never as
	// internal design notes.
	for name, text := range map[string]string{
		"config": string(configData),
		"schema": string(data),
	} {
		if strings.Contains(text, "ADR") {
			t.Errorf("%s mentions an ADR", name)
		}
	}
	if !strings.Contains(string(data), "Config is the root document") {
		t.Error("the generated schema lost its descriptions")
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := initConfig(path, false); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if err := initConfig(path, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("second init = %v, want a --force error", err)
	}
	if err := initConfig(path, true); err != nil {
		t.Fatalf("forced init: %v", err)
	}
}

func TestInitRefusesAnExistingSchemaAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, schemaFileName),
		[]byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yml")
	if err := initConfig(path, false); err == nil {
		t.Fatal("want an error when only the schema exists")
	}
}

func TestInitDefaultPathIsXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path, err := defaultConfigPath()
	if err != nil {
		t.Fatalf("defaultConfigPath: %v", err)
	}
	if want := filepath.Join(xdg, "botflota", "config.yml"); path != want {
		t.Fatalf("default path = %q, want %q", path, want)
	}
}
