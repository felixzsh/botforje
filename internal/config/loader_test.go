package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLoader() Loader {
	values := map[string]string{
		"OPENAI_API_KEY": "sk-test",
		"API_TOKEN":      "token",
	}
	return Loader{LookupEnv: func(name string) (string, bool) {
		v, ok := values[name]
		return v, ok
	}}
}

func writeTemp(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadExample(t *testing.T) {
	cfg, err := testLoader().Load("../../config.example.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if errs := Validate(cfg); len(errs) != 0 {
		t.Fatalf("validate returned errors: %v", errs)
	}
	if cfg.LLM == nil || cfg.LLM.APIKey.Reveal() != "sk-test" {
		t.Fatalf("api_key was not interpolated from the environment")
	}
}

func TestInterpolationMissing(t *testing.T) {
	path := writeTemp(t, t.TempDir(), "config.yml",
		"version: 1\nllm:\n  api_key: \"${MISSING_VAR}\"\n")
	_, err := testLoader().Load(path)
	if err == nil || !strings.Contains(err.Error(), "MISSING_VAR") {
		t.Fatalf("want missing variable error, got %v", err)
	}
}

func TestStrictUnknownField(t *testing.T) {
	path := writeTemp(t, t.TempDir(), "config.yml", "version: 1\nnope: true\n")
	if _, err := testLoader().Load(path); err == nil {
		t.Fatal("want an error for the unknown field")
	}
}

func TestIncludeMerge(t *testing.T) {
	cfg, err := testLoader().Load("testdata/include/config.yml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := cfg.Actions["base"]; !ok {
		t.Error("main action missing")
	}
	if _, ok := cfg.Actions["extra"]; !ok {
		t.Error("included action missing")
	}
}

func TestIncludeDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "frag.yml",
		"actions:\n  dup:\n    steps:\n      - message:\n          body: \"x\"\n")
	main := writeTemp(t, dir, "config.yml",
		"version: 1\ninclude:\n  - ./frag.yml\nactions:\n  dup:\n    steps:\n      - message:\n          body: \"y\"\n")
	_, err := testLoader().Load(main)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestSecretMasks(t *testing.T) {
	s := Secret("super-secret")
	if s.String() != "***" || s.GoString() != "***" {
		t.Fatalf("secret leaks through formatting: %q %q", s.String(), s.GoString())
	}
	if s.Reveal() != "super-secret" {
		t.Fatal("Reveal must return the real value")
	}
}
