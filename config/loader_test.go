package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testLoader() Loader {
	values := map[string]string{
		"OPENAI_API_KEY":          "sk-test",
		"API_TOKEN":               "token",
		"BOTFLOTA_WEBHOOK_SECRET": "webhook-secret",
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
	if errs := Validate(cfg, testCatalog(t)); len(errs) != 0 {
		t.Fatalf("validate returned errors: %v", errs)
	}
	if llm := cfg.Scopes["support"].LLMs["primary"]; llm.APIKey.Reveal() != "sk-test" {
		t.Fatalf("api_key was not interpolated from the environment")
	}
	if len(cfg.Scopes["support"].Actions) == 0 {
		t.Fatal("expected actions under the support scope")
	}
}

func TestScopeDisplayNameMergesFromIncludes(t *testing.T) {
	dir := t.TempDir()
	main := writeTemp(t, dir, "config.yml", `include: [scope.yml]
scopes:
  support:
    bots:
      assistant:
        graph: welcome
`)
	writeTemp(t, dir, "scope.yml", `scopes:
  support:
    display_name: Support team
    graphs:
      welcome:
        root: hello
        nodes:
          hello: {}
`)

	cfg, err := testLoader().Load(main)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Scopes["support"].DisplayName; got != "Support team" {
		t.Fatalf("scope display_name = %q, want Support team", got)
	}
}

func TestConflictingScopeDisplayNamesFail(t *testing.T) {
	dir := t.TempDir()
	main := writeTemp(t, dir, "config.yml", `include: [scope.yml]
scopes:
  support:
    display_name: Support
`)
	writeTemp(t, dir, "scope.yml", `scopes:
  support:
    display_name: Help desk
`)

	_, err := testLoader().Load(main)
	if err == nil || !strings.Contains(err.Error(), "conflicting display_name") {
		t.Fatalf("Load error = %v, want conflicting scope display_name", err)
	}
}

func TestWatchPathsIncludeTheBaseOfGlobIncludes(t *testing.T) {
	dir := t.TempDir()
	parts := filepath.Join(dir, "parts")
	if err := os.MkdirAll(filepath.Join(parts, "one"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTemp(t, filepath.Join(parts, "one"), "scope.yml",
		"scopes:\n  support: {}\n")
	main := writeTemp(t, dir, "config.yml",
		"include: [parts/*/*.yml]\nscopes: {}\n")
	cfg, err := testLoader().Load(main)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !slices.Contains(cfg.WatchPaths(), parts) {
		t.Fatalf("WatchPaths = %v, want glob base %q", cfg.WatchPaths(), parts)
	}
}

func TestInterpolationMissing(t *testing.T) {
	path := writeTemp(t, t.TempDir(), "config.yml", `scopes:
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
	path := writeTemp(t, t.TempDir(), "config.yml", "nope: true\n")
	if _, err := testLoader().Load(path); err == nil {
		t.Fatal("want an error for the unknown field")
	}
}

// TestTopLevelDeviceNameHasNoAlias: device_name is an adapter-owned channel
// option, so the retired instance-level key fails as an unknown field with no
// alias or no-op (ADR 0054).
func TestTopLevelDeviceNameHasNoAlias(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "alone",
			body: "device_name: \"Acme\"\n",
		},
		{
			name: "beside instance settings",
			body: "port: 3000\ndevice_name: \"Acme\"\nscopes: {}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, t.TempDir(), "config.yml", tc.body)
			_, err := testLoader().Load(path)
			if err == nil || !strings.Contains(err.Error(), "device_name") ||
				!strings.Contains(err.Error(), "not found") {
				t.Fatalf("want a not-found device_name error, got %v", err)
			}
		})
	}
}

func TestSameNameAcrossScopes(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "config.yml", `scopes:
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
	if errs := Validate(cfg, testCatalog(t)); len(errs) != 0 {
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
	main := writeTemp(t, dir, "config.yml", `include:
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

func TestSecretRefKeepsPlaceholder(t *testing.T) {
	path := writeTemp(t, t.TempDir(), "config.yml", `scopes:
  default:
    llms:
      primary:
        provider: openai
        model: gpt
        api_key: "${OPENAI_API_KEY}"
    actions:
      call:
        steps:
          - request:
              name: ping
              url: "http://example.test"
              headers:
                Authorization: "Bearer ${API_TOKEN}"
`)
	cfg, err := testLoader().Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if errs := Validate(cfg, testCatalog(t)); len(errs) != 0 {
		t.Fatalf("validate returned errors: %v", errs)
	}
	llm := cfg.Scopes["default"].LLMs["primary"]
	if llm.APIKey.Reveal() != "sk-test" {
		t.Fatalf("api_key Reveal = %q, want the resolved value", llm.APIKey.Reveal())
	}
	if llm.APIKey.String() != "${OPENAI_API_KEY}" || llm.APIKey.Raw() != "${OPENAI_API_KEY}" {
		t.Fatalf("api_key lost its placeholder: %q %q",
			llm.APIKey.String(), llm.APIKey.Raw())
	}
	header := cfg.Scopes["default"].Actions["call"].Steps[0].Request.Headers["Authorization"]
	if header.Reveal() != "Bearer token" {
		t.Fatalf("header Reveal = %q, want the resolved value", header.Reveal())
	}
	if header.String() != "Bearer ${API_TOKEN}" || !header.HasRef() {
		t.Fatalf("header lost its placeholder: %q hasRef=%v",
			header.String(), header.HasRef())
	}
}

// TestSecretRefOutsideSecretFieldRejected checks that an ordinary string
// keeps its `${...}` text at load and validation reports it: a secret can
// never be expanded into a message, a URL, or a context value (ADR 0014).
func TestSecretRefOutsideSecretFieldRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "message body",
			body: `    actions:
      call:
        steps: [{message: {body: "token ${API_TOKEN}"}}]
`,
			want: "steps[0].message.body[0]",
		},
		{
			name: "request url",
			body: `    actions:
      call:
        steps:
          - request: {name: ping, url: "http://example.test/${API_TOKEN}"}
`,
			want: "steps[0].request.url",
		},
		{
			name: "graph context",
			body: `    graphs:
      menu:
        initial_context: {token: "${API_TOKEN}"}
        nodes: {start: {}}
`,
			want: "initial_context[token]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, t.TempDir(), "config.yml",
				"scopes:\n  default:\n"+tc.body)
			cfg, err := testLoader().Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			errs := Validate(cfg, testCatalog(t))
			if len(errs) == 0 {
				t.Fatal("want a validation error for the secret reference")
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.want) &&
					strings.Contains(e.Error(), "secret reference") {
					found = true
				}
			}
			if !found {
				t.Fatalf("errors = %v, want one naming %q", errs, tc.want)
			}
		})
	}
}

// TestSecretRefInUnownedSettingsRejected: instance settings and a scope's
// visible label are not interpolated either, so a reference there is refused
// with the same rule (ADR 0014).
func TestSecretRefInUnownedSettingsRejected(t *testing.T) {
	const scope = `scopes:
  default:
    actions:
      call:
        steps: [{message: {body: "hi"}}]
`
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "instance address",
			doc:  "address: \"${API_TOKEN}\"\n" + scope,
			want: "address",
		},
		{
			name: "data dir",
			doc:  "data_dir: \"${API_TOKEN}\"\n" + scope,
			want: "data_dir",
		},
		{
			name: "scope display name",
			doc: `scopes:
  default:
    display_name: "${API_TOKEN}"
    actions:
      call:
        steps: [{message: {body: "hi"}}]
`,
			want: "display_name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, t.TempDir(), "config.yml", tc.doc)
			cfg, err := testLoader().Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			errs := Validate(cfg, testCatalog(t))
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.want) &&
					strings.Contains(e.Error(), "secret reference") {
					found = true
				}
			}
			if !found {
				t.Fatalf("errors = %v, want one naming %q", errs, tc.want)
			}
		})
	}
}

// TestIncludeWithSecretRefRejected: an include path is not interpolated, so a
// reference in it is refused before the path is resolved.
func TestIncludeWithSecretRefRejected(t *testing.T) {
	path := writeTemp(t, t.TempDir(), "config.yml",
		"include:\n  - \"${DIR}/scopes.yml\"\n")
	_, err := testLoader().Load(path)
	if err == nil {
		t.Fatal("want the reference in an include path refused")
	}
	if !strings.Contains(err.Error(), "secret reference") ||
		!strings.Contains(err.Error(), "include") {
		t.Fatalf("error = %v, want a secret-reference refusal naming the include", err)
	}
}

// TestSecretRefMissingVariable checks that a malformed or unset reference in a
// secret field fails at load instead of being sent literally.
func TestSecretRefMissingVariable(t *testing.T) {
	path := writeTemp(t, t.TempDir(), "config.yml", `scopes:
  default:
    actions:
      call:
        steps:
          - request:
              name: ping
              url: "http://example.test"
              headers:
                Authorization: "Bearer ${NOT_SET_ANYWHERE}"
`)
	_, err := testLoader().Load(path)
	if err == nil || !strings.Contains(err.Error(), "NOT_SET_ANYWHERE") {
		t.Fatalf("want a missing variable error, got %v", err)
	}
}
