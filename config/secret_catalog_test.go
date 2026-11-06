package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/felixzsh/botflota/spec"
)

// yamlMap renders one instance-level map setting as main-file lines.
func yamlMap(name string, entries map[string]string) []string {
	if len(entries) == 0 {
		return nil
	}
	lines := []string{name + ":"}
	for _, key := range SortedKeys(entries) {
		lines = append(lines, fmt.Sprintf("  %s: %q", key, entries[key]))
	}
	return lines
}

// catalogConfig writes a main file holding the given instance-level lines and
// one scope "alice" whose model key is the given reference, so a load failure
// or a resolved value belongs to that scope's catalog.
func catalogConfig(
	t *testing.T, dir string, lines []string, key string,
) string {
	t.Helper()
	body := append([]string{}, lines...)
	body = append(body, strings.Split(strings.Join([]string{
		"scopes:",
		"  alice:",
		"    llms:",
		"      primary:",
		"        provider: openai",
		"        model: gpt-4o-mini",
		fmt.Sprintf("        api_key: %q", key),
		"",
	}, "\n"), "\n")...)
	return writeTemp(t, dir, "config.yml", strings.Join(body, "\n"))
}

// envOnly is a loader whose environment holds exactly these names.
func envOnly(values map[string]string) Loader {
	return Loader{LookupEnv: func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}}
}

// TestLoadSecretCatalog proves ADR 0049's resolution rule: a scope with a
// catalog resolves only from it, a scope without one keeps the process
// environment, and a name the catalog lacks fails with the name and no value.
func TestLoadSecretCatalog(t *testing.T) {
	cases := []struct {
		name      string
		catalog   string
		declared  bool
		catalogAt string
		key       string
		env       map[string]string
		wantValue string
		wantErr   string
		wantGone  []string
	}{
		{name: "a catalog value resolves instead of the environment",
			catalog: "API_KEY=sk-catalog\n", declared: true, key: "${API_KEY}",
			env:       map[string]string{"API_KEY": "sk-environment"},
			wantValue: "sk-catalog"},
		{name: "a name only the environment holds fails under a catalog",
			catalog: "OTHER=sk-other\n", declared: true, key: "${API_KEY}",
			env:     map[string]string{"API_KEY": "sk-environment"},
			wantErr: "API_KEY", wantGone: []string{"sk-environment", "sk-other"}},
		{name: "an unheld name leaks no other catalog value",
			catalog: "API_KEY=sk-catalog\nOTHER=sk-other\n", declared: true,
			key: "${MISSING}", wantErr: "MISSING",
			wantGone: []string{"sk-catalog", "sk-other"}},
		{name: "comments, blank lines, and = inside a value parse",
			catalog: "# a comment\n\nAPI_KEY=a=b\n", declared: true,
			key: "${API_KEY}", wantValue: "a=b"},
		{name: "a scope without a catalog keeps the process environment",
			key: "${API_KEY}", env: map[string]string{"API_KEY": "sk-environment"},
			wantValue: "sk-environment"},
		{name: "a missing catalog file is refused",
			declared: true, catalogAt: "gone.env", key: "${API_KEY}",
			wantErr: "no such file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			secretFiles := map[string]string{}
			if tc.declared {
				secretFiles["alice"] = writeTemp(t, dir, "catalog.env", tc.catalog)
			}
			if tc.catalogAt != "" {
				secretFiles["alice"] = filepath.Join(dir, tc.catalogAt)
			}
			main := catalogConfig(t, dir, yamlMap("secret_files", secretFiles), tc.key)
			cfg, err := envOnly(tc.env).Load(main)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("load = %v, want an error naming %q", cfg, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("load error %v does not name %q", err, tc.wantErr)
				}
				for _, value := range tc.wantGone {
					if strings.Contains(err.Error(), value) {
						t.Fatalf("load error %v leaks %q", err, value)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			key := cfg.Scopes["alice"].LLMs["primary"].APIKey
			if key.Reveal() != tc.wantValue {
				t.Fatalf("api_key = %q, want %q", key.Reveal(), tc.wantValue)
			}
			// ADR 0014 still holds: the secret keeps the placeholder as written
			// and serialization reveals no value.
			if key.Raw() != tc.key {
				t.Fatalf("api_key as written = %q, want %q", key.Raw(), tc.key)
			}
			encoded, err := encode(cfg)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if !strings.Contains(string(encoded), tc.key) {
				t.Fatalf("encoded config lost the placeholder:\n%s", encoded)
			}
			if !strings.Contains(tc.wantValue, "${") &&
				strings.Contains(string(encoded), tc.wantValue) {
				t.Fatalf("encoded config holds the value:\n%s", encoded)
			}
		})
	}
}

// TestSecretCatalogHeaderValue proves a request header resolves from the same
// catalog as a model key: both are secret fields (ADR 0014).
func TestSecretCatalogHeaderValue(t *testing.T) {
	dir := t.TempDir()
	catalog := writeTemp(t, dir, "catalog.env", "TOKEN=bearer-catalog\n")
	body := strings.Join([]string{
		"secret_files:",
		fmt.Sprintf("  alice: %q", catalog),
		"scopes:",
		"  alice:",
		"    actions:",
		"      call:",
		"        steps:",
		"          - request:",
		"              name: call",
		"              method: GET",
		"              url: https://example.com/hook",
		"              headers:",
		"                Authorization: ${TOKEN}",
		"",
	}, "\n")
	main := writeTemp(t, dir, "config.yml", body)
	cfg, err := envOnly(map[string]string{"TOKEN": "bearer-environment"}).Load(main)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	header := cfg.Scopes["alice"].Actions["call"].Steps[0].Request.Headers["Authorization"]
	if header.Reveal() != "bearer-catalog" {
		t.Fatalf("header = %q, want the catalog value", header.Reveal())
	}
	if header.Raw() != "${TOKEN}" {
		t.Fatalf("header as written = %q, want the placeholder", header.Raw())
	}
}

func TestParseSecretCatalog(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    secretCatalog
		wantErr string
	}{
		{name: "empty file", content: "", want: secretCatalog{}},
		{name: "comments and blank lines", content: "# one\n\n   \nA=1\n  # two\n",
			want: secretCatalog{"A": "1"}},
		{name: "an = inside a value", content: "A=b=c\n",
			want: secretCatalog{"A": "b=c"}},
		{name: "carriage returns", content: "A=1\r\nB=2\r\n",
			want: secretCatalog{"A": "1", "B": "2"}},
		{name: "a later duplicate wins", content: "A=1\nA=2\n",
			want: secretCatalog{"A": "2"}},
		{name: "the value is kept as written", content: "A= value \n",
			want: secretCatalog{"A": " value "}},
		{name: "a line without =", content: "# ok\nnoequals\n", wantErr: "line 2"},
		{name: "an empty name", content: "=value\n", wantErr: "line 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSecretCatalog([]byte(tc.content))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parse = %v, %v; want an error mentioning %q",
						got, err, tc.wantErr)
				}
				if !strings.Contains(err.Error(), "line ") {
					t.Fatalf("parse error %v does not name the line", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parse = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSecretCatalogOutsideFileRoots proves a catalog may not sit inside any
// scope's file root, where a media step could send it (ADR 0047, ADR 0049).
func TestSecretCatalogOutsideFileRoots(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "assets")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	otherRoot := filepath.Join(base, "other-assets")
	if err := os.MkdirAll(otherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := writeTemp(t, base, "alice.env", "API_KEY=sk-catalog\n")
	inside := writeTemp(t, root, "alice.env", "API_KEY=sk-catalog\n")
	insideNested := writeTemp(t, nested, "alice.env", "API_KEY=sk-catalog\n")
	link := filepath.Join(base, "link.env")
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	cases := []struct {
		name   string
		roots  map[string]string
		catalo map[string]string
		want   string
	}{
		{name: "a catalog outside every root loads",
			roots:  map[string]string{"alice": root},
			catalo: map[string]string{"alice": outside}},
		{name: "a catalog inside the scope's own root is refused",
			roots:  map[string]string{"alice": root},
			catalo: map[string]string{"alice": inside}, want: "inside file_roots"},
		{name: "a catalog in a subdirectory of a root is refused",
			roots:  map[string]string{"alice": root},
			catalo: map[string]string{"alice": insideNested}, want: "inside file_roots"},
		{name: "a catalog inside another scope's root is refused",
			roots:  map[string]string{"alice": otherRoot, "bob": root},
			catalo: map[string]string{"alice": inside}, want: "inside file_roots"},
		{name: "a symbolic link into a root is refused",
			roots:  map[string]string{"alice": root},
			catalo: map[string]string{"alice": link}, want: "inside file_roots"},
		{name: "an empty path is refused",
			catalo: map[string]string{"alice": ""}, want: "names no catalog"},
		{name: "a reference in the path is refused",
			catalo: map[string]string{"alice": "${HOME}/alice.env"},
			want:   "secret reference is only allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := append(yamlMap("file_roots", tc.roots),
				yamlMap("secret_files", tc.catalo)...)
			main := catalogConfig(t, t.TempDir(), lines, "${API_KEY}")
			_, err := testLoader().Load(main)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("load = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// TestSecretCatalogRestartRequired proves the owner's restart adopts a catalog,
// whether the declaration or the file's content changed (ADR 0049).
func TestSecretCatalogRestartRequired(t *testing.T) {
	t.Run("an unchanged catalog is no change", func(t *testing.T) {
		dir := t.TempDir()
		catalog := writeTemp(t, dir, "alice.env", "API_KEY=one\n")
		main := catalogConfig(t, dir,
			yamlMap("secret_files", map[string]string{"alice": catalog}), "${API_KEY}")
		current, err := testLoader().Load(main)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		next, err := testLoader().Load(main)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if plan := PlanReload(current, next); len(plan.Global) != 0 ||
			len(plan.Scopes) != 0 {
			t.Fatalf("plan = %+v, want no change", plan)
		}
	})
	t.Run("declaring a catalog requires a restart", func(t *testing.T) {
		dir := t.TempDir()
		catalog := writeTemp(t, dir, "alice.env", "API_KEY=one\n")
		before, err := envOnly(map[string]string{"API_KEY": "one"}).
			Load(catalogConfig(t, dir, nil, "${API_KEY}"))
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		after, err := testLoader().Load(catalogConfig(t, dir,
			yamlMap("secret_files", map[string]string{"alice": catalog}), "${API_KEY}"))
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		plan := PlanReload(before, after)
		if !containsReason(plan.Global, "secret_files") {
			t.Fatalf("plan.Global = %v, want a restart for secret_files", plan.Global)
		}
	})
	t.Run("rotating a catalog requires a restart", func(t *testing.T) {
		dir := t.TempDir()
		catalog := writeTemp(t, dir, "alice.env", "API_KEY=one\n")
		main := catalogConfig(t, dir,
			yamlMap("secret_files", map[string]string{"alice": catalog}), "${API_KEY}")
		current, err := testLoader().Load(main)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		writeTemp(t, dir, "alice.env", "API_KEY=two\n")
		next, err := testLoader().Load(main)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		plan := PlanReload(current, next)
		if !containsReason(plan.Global, "secret_files") {
			t.Fatalf("plan.Global = %v, want a restart for the rotation", plan.Global)
		}
	})
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}

// TestInstanceSecretsResolveFromEnvironment proves an instance-level secret
// stays owner-owned: no scope document can reach it, so it resolves from the
// process environment even while a scope has a catalog (ADR 0049).
func TestInstanceSecretsResolveFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	catalog := writeTemp(t, dir, "alice.env", "API_KEY=sk-catalog\n")
	lines := append(yamlMap("secret_files", map[string]string{"alice": catalog}),
		"webhooks:",
		"  - url: https://example.com/events",
		"    secret: ${BOTFLOTA_WEBHOOK_SECRET}",
		"    events: [message]",
	)
	cfg, err := testLoader().Load(catalogConfig(t, dir, lines, "${API_KEY}"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Webhooks[0].Secret.Reveal(); got != "webhook-secret" {
		t.Fatalf("webhook secret = %q, want the environment value", got)
	}
	if got := cfg.Webhooks[0].Secret.Raw(); got != "${BOTFLOTA_WEBHOOK_SECRET}" {
		t.Fatalf("webhook secret as written = %q, want the placeholder", got)
	}
}

func TestScopedDocumentCannotSetSecretFiles(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "scopes.yml",
		"secret_files:\n  alice: /tmp/x\nscopes: {}\n")
	main := writeTemp(t, dir, "config.yml",
		"include:\n  - scopes.yml\nscopes: {}\n")
	_, err := testLoader().Load(main)
	if err == nil || !strings.Contains(err.Error(), "secret_files") {
		t.Fatalf("load = %v, want secret_files refused in a fragment", err)
	}
}

// TestPublishKeepsTheOwnerSecretCatalog proves a scoped publication changes no
// instance setting: the main file keeps its secret_files map, and the scope
// still resolves only from that catalog afterwards.
func TestPublishKeepsTheOwnerSecretCatalog(t *testing.T) {
	dir := t.TempDir()
	catalog := writeTemp(t, dir, "catalog.env", "API_KEY=sk-catalog\n")
	body := strings.Join([]string{
		"secret_files:",
		fmt.Sprintf("  support: %q", catalog),
		"scopes:",
		"  support:",
		"    llms:",
		"      primary:",
		"        provider: openai",
		"        model: gpt-4o-mini",
		"        api_key: ${API_KEY}",
		"    graphs:",
		"      main:",
		"        root: hello",
		"        nodes:",
		"          hello:",
		"            goal: say hello",
		"    bots:",
		"      demo:",
		"        graph: main",
		"        channel:",
		"          provider: whatsapp",
		"          mode: native",
		"",
	}, "\n")
	main := writeTemp(t, dir, "config.yml", body)
	writer, err := NewWriter(main, testCatalog(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	current, err := (Loader{}).Load(main)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	next := copyScope(current.Scopes["support"])
	next.Graphs["main"] = spec.Graph{Root: "hello", Nodes: map[string]spec.Node{
		"hello": {Goal: "say hello again"},
	}}
	if _, err := writer.Publish(context.Background(), Publication{
		Scopes: map[string]ScopeWrite{
			"support": {Version: scopeVersion(t, current, "support"), Scope: next},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	written := readFileString(t, main)
	if !strings.Contains(written, "secret_files:") {
		t.Fatalf("the publication dropped the owner's secret_files:\n%s", written)
	}
	if strings.Contains(written, "sk-catalog") {
		t.Fatalf("the publication wrote a catalog value:\n%s", written)
	}
	after, err := (Loader{}).Load(main)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reflect.DeepEqual(after.SecretFiles, current.SecretFiles) {
		t.Fatalf("secret_files = %v, want %v", after.SecretFiles, current.SecretFiles)
	}
	key := after.Scopes["support"].LLMs["primary"].APIKey
	if key.Raw() != "${API_KEY}" || key.Reveal() != "sk-catalog" {
		t.Fatalf("api_key = %q (%q), want the catalog value behind the placeholder",
			key.Raw(), key.Reveal())
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
