package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixzsh/botflota/spec"
)

// rootsConfig writes a main file whose only content is an instance-level
// file_roots map, so any load failure belongs to the roots.
func rootsConfig(t *testing.T, roots map[string]string) string {
	t.Helper()
	var body strings.Builder
	body.WriteString("file_roots:\n")
	for _, name := range SortedKeys(roots) {
		fmt.Fprintf(&body, "  %s: %q\n", name, roots[name])
	}
	body.WriteString("scopes: {}\n")
	return writeTemp(t, t.TempDir(), "config.yml", body.String())
}

func TestLoadFileRoots(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "private")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	fileRoot := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(fileRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(inside, "nested")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	// A root that is a symbolic link to another root names the same directory,
	// which is as overlapping as a literal duplicate.
	link := filepath.Join(base, "link")
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	cases := []struct {
		name  string
		roots map[string]string
		want  string
	}{
		{name: "an absolute existing directory loads", roots: map[string]string{
			"mine": inside,
		}},
		{name: "two distinct roots load", roots: map[string]string{
			"mine": inside, "yours": other,
		}},
		{name: "a relative root is rejected", roots: map[string]string{
			"mine": "private",
		}, want: "must be an absolute directory"},
		{name: "a missing root is rejected", roots: map[string]string{
			"mine": filepath.Join(base, "gone"),
		}, want: "no such file"},
		{name: "a file is not a root", roots: map[string]string{
			"mine": fileRoot,
		}, want: "is not a directory"},
		{name: "a root inside another root is rejected", roots: map[string]string{
			"mine": inside, "yours": child,
		}, want: "overlap"},
		{name: "a root containing another root is rejected", roots: map[string]string{
			"mine": child, "yours": inside,
		}, want: "overlap"},
		{name: "the same root twice is rejected", roots: map[string]string{
			"mine": inside, "yours": inside,
		}, want: "overlap"},
		{name: "a symbolic link to another root is rejected", roots: map[string]string{
			"mine": inside, "yours": link,
		}, want: "overlap"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := rootsConfig(t, tc.roots)
			cfg, err := testLoader().Load(path)
			if err == nil {
				if tc.want != "" {
					t.Fatalf("loaded %v, want an error mentioning %q", tc.roots, tc.want)
				}
				if len(cfg.FileRoots) != len(tc.roots) {
					t.Fatalf("file roots = %v, want %v", cfg.FileRoots, tc.roots)
				}
				return
			}
			if tc.want == "" {
				t.Fatalf("load: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("load error %v does not mention %q", err, tc.want)
			}
		})
	}
}

// TestFileRootsAreNotInterpolated: a root is a literal absolute directory, so
// a reference in one is refused rather than resolved to something else.
func TestFileRootsAreNotInterpolated(t *testing.T) {
	path := writeTemp(t, t.TempDir(), "config.yml",
		"file_roots:\n  mine: ${HOME}\nscopes: {}\n")
	_, err := testLoader().Load(path)
	if err == nil || !strings.Contains(err.Error(), "must be an absolute directory") {
		t.Fatalf("load = %v, want a literal-directory refusal", err)
	}
}

func TestValidateFileRoots(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name  string
		roots map[string]string
		want  string
	}{
		{name: "a root for a declared scope", roots: map[string]string{
			"mine": root,
		}},
		{name: "a root for an undeclared scope is rejected", roots: map[string]string{
			"other": root,
		}, want: `file_roots names unknown scope "other"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{FileRoots: tc.roots, Scopes: map[string]spec.Scope{
				"mine": {},
			}}
			errs := Validate(cfg, testCatalog(t))
			found := false
			for _, e := range errs {
				found = found || strings.Contains(e.Error(), tc.want)
			}
			if tc.want == "" && len(errs) != 0 {
				t.Fatalf("validate = %v", errs)
			}
			if tc.want != "" && !found {
				t.Fatalf("no error mentions %q; got %v", tc.want, errs)
			}
		})
	}
}

// TestMediaPathUnderFileRoot pins the early half of ADR 0047: under a root a
// literal absolute path is rejected at validation, while the reader still
// enforces the rule for a templated one.
func TestMediaPathUnderFileRoot(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		root string
		path string
		want string
	}{
		{name: "absolute path under a root", root: root,
			path: "/etc/passwd", want: "is absolute"},
		{name: "relative path under a root", root: root, path: "uploads/photo.png"},
		{name: "templated path under a root", root: root,
			path: "{{node.file}}"},
		{name: "absolute path without a root keeps today's behavior",
			path: "/etc/passwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Scopes: map[string]spec.Scope{
				"mine": {Actions: map[string]spec.Action{
					"send": {Steps: []spec.Step{{Media: &spec.Media{
						Kind: "image", Path: tc.path,
					}}}},
				}},
			}}
			if tc.root != "" {
				cfg.FileRoots = map[string]string{"mine": tc.root}
			}
			errs := Validate(cfg, testCatalog(t))
			found := false
			for _, e := range errs {
				found = found || strings.Contains(e.Error(), tc.want)
			}
			if tc.want == "" && found {
				t.Fatalf("media path %q was refused under its root: %v", tc.path, errs)
			}
			if tc.want != "" && !found {
				t.Fatalf("no error mentions %q; got %v", tc.want, errs)
			}
		})
	}
}
