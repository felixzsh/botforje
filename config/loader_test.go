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
	cfg, err := testLoader().Load("examples/reference.yml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if errs := Validate(cfg); len(errs) != 0 {
		t.Fatalf("validate returned errors: %v", errs)
	}
	if llm := cfg.Scopes["support"].LLMs["primary"]; llm.APIKey.Reveal() != "sk-test" {
		t.Fatalf("api_key was not interpolated from the environment")
	}
	if len(cfg.Scopes["support"].Actions) == 0 {
		t.Fatal("expected actions under the support scope")
	}
}

func TestInterpolationMissing(t *testing.T) {
	path := writeTemp(t, t.TempDir(), "config.yml", `version: 1
scopes:
  default:
    llms:
      a:
        model: gpt
        api_key: "${MISSING_VAR}"
`)
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

func TestSameNameAcrossScopes(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "config.yml", `version: 1
scopes:
  client-a:
    actions:
      pricing:
        steps: [{message: {body: "a"}}]
  client-b:
    actions:
      pricing:
        steps: [{message: {body: "b"}}]
`)
	cfg, err := testLoader().Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if errs := Validate(cfg); len(errs) != 0 {
		t.Fatalf("same name in different scopes must be valid: %v", errs)
	}
}

func TestIncludeMerge(t *testing.T) {
	cfg, err := testLoader().Load("testdata/include/config.yml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	actions := cfg.Scopes["default"].Actions
	if _, ok := actions["base"]; !ok {
		t.Error("main action missing")
	}
	if _, ok := actions["extra"]; !ok {
		t.Error("included action missing")
	}
}

func TestIncludeDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "frag.yml", `scopes:
  default:
    actions:
      dup:
        steps: [{message: {body: "x"}}]
`)
	main := writeTemp(t, dir, "config.yml", `version: 1
include:
  - ./frag.yml
scopes:
  default:
    actions:
      dup:
        steps: [{message: {body: "y"}}]
`)
	_, err := testLoader().Load(main)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestNestedIncludeAndCycle(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "b.yml", `include:
  - ./a.yml
scopes:
  default:
    actions:
      from-b:
        steps: [{message: {body: "b"}}]
`)
	main := writeTemp(t, dir, "a.yml", `include:
  - ./b.yml
scopes:
  default:
    actions:
      from-a:
        steps: [{message: {body: "a"}}]
`)
	cfg, err := testLoader().Load(main)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	actions := cfg.Scopes["default"].Actions
	if _, ok := actions["from-a"]; !ok {
		t.Error("missing from-a (main)")
	}
	if _, ok := actions["from-b"]; !ok {
		t.Error("missing from-b (nested include)")
	}
}

func TestIncludeFileAndDir(t *testing.T) {
	fileCfg, err := testLoader().Load("testdata/include-file/config.yml")
	if err != nil {
		t.Fatalf("load file include: %v", err)
	}
	for _, want := range []string{"base", "from-file"} {
		if _, ok := fileCfg.Scopes["default"].Actions[want]; !ok {
			t.Errorf("file include missing action %q", want)
		}
	}

	dirCfg, err := testLoader().Load("testdata/include-dir/config.yml")
	if err != nil {
		t.Fatalf("load dir include: %v", err)
	}
	for _, want := range []string{"base", "one"} {
		if _, ok := dirCfg.Scopes["default"].Actions[want]; !ok {
			t.Errorf("dir include missing default action %q", want)
		}
	}
	if _, ok := dirCfg.Scopes["support"].Actions["two"]; !ok {
		t.Error("dir include did not bring the support scope")
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
