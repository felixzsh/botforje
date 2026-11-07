package botflota

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const fixtureRoot = "testdata/fileroot"

// outsideTree lays out a root directory beside a file it must not reach:
//
//	tmp/root/notes.txt
//	tmp/outside.txt
//	tmp/root/escape -> ../outside.txt
//	tmp/root/link -> notes.txt
func outsideTree(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"),
		[]byte("inside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "outside.txt"),
		[]byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside.txt", filepath.Join(root, "escape")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if err := os.Symlink("notes.txt", filepath.Join(root, "link")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	return root
}

func readOpened(t *testing.T, root, path string) (string, error) {
	t.Helper()
	file, err := OpenLocalFile(root, path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func TestOpenLocalFile(t *testing.T) {
	root := outsideTree(t)
	unreadable := filepath.Join(root, "unreadable.txt")
	if err := os.WriteFile(unreadable, []byte("hidden\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unreadable); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		root    string
		path    string
		want    string
		outside bool
		wantErr bool
	}{
		{name: "relative resolves under the root", root: fixtureRoot,
			path: "notes.txt", want: "inside\n"},
		{name: "nested relative resolves under the root", root: fixtureRoot,
			path: "nested/inner.txt", want: "inner\n"},
		{name: "an inner segment that returns stays inside", root: fixtureRoot,
			path: "nested/../notes.txt", want: "inside\n"},
		{name: "absolute rejected", root: fixtureRoot,
			path: "/etc/passwd", outside: true, wantErr: true},
		{name: "reading above the root refused", root: root,
			path: "../outside.txt", outside: true, wantErr: true},
		{name: "a symbolic link out of the root refused", root: root,
			path: "escape", outside: true, wantErr: true},
		{name: "a symbolic link inside the root resolves", root: root,
			path: "link", want: "inside\n"},
		{name: "unreadable file fails", root: root,
			path: "unreadable.txt", wantErr: true},
		{name: "missing file fails", root: root,
			path: "missing.txt", wantErr: true},
		{name: "missing root fails", root: filepath.Join(root, "gone"),
			path: "notes.txt", wantErr: true},
		{name: "empty path fails", root: root, path: "", wantErr: true},
		{name: "no root keeps the process's permissions", root: "",
			path: filepath.Join(fixtureRoot, "notes.txt"), want: "inside\n"},
		{name: "no root opens a relative path as written", root: "",
			path: filepath.Join(fixtureRoot, "nested", "inner.txt"), want: "inner\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantErr && tc.path == "unreadable.txt" && os.Geteuid() == 0 {
				t.Skip("the process can read any file as root")
			}
			got, err := readOpened(t, tc.root, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("opened %q = %q, want a refusal", tc.path, got)
				}
				if tc.outside && !errors.Is(err, ErrOutsideFileRoot) {
					t.Fatalf("error %v is not a file-root refusal", err)
				}
				if !tc.outside && errors.Is(err, ErrOutsideFileRoot) {
					t.Fatalf("error %v reports an escape it is not", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("open %q: %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("read %q = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// insideLinks adds the links an operator provisions inside a root: an absolute
// link to a directory, and one to a file, both pointing back into the root.
//
//	root/media -> <root>/v3            (absolute, inside)
//	root/v3/current -> <root>/v3/photo.png (absolute, inside)
func insideLinks(t *testing.T, root string) {
	t.Helper()
	v3 := filepath.Join(root, "v3")
	if err := os.MkdirAll(v3, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v3, "photo.png"),
		[]byte("v3-bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(v3, filepath.Join(root, "media")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(v3, "photo.png"),
		filepath.Join(v3, "current")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
}

// TestOpenLocalFileFollowsInRootLinks: an absolute link that stays inside the
// root is legitimate, so a step reads its target instead of failing delivery
// (ADR 0047).
func TestOpenLocalFileFollowsInRootLinks(t *testing.T) {
	root := outsideTree(t)
	insideLinks(t, root)
	cases := []struct {
		name string
		path string
		want string
	}{
		{name: "an absolute link to a directory", path: "media/photo.png",
			want: "v3-bytes\n"},
		{name: "an absolute link to a file", path: "v3/current",
			want: "v3-bytes\n"},
		{name: "a relative path that reaches the link", path: "v3/../media/photo.png",
			want: "v3-bytes\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readOpened(t, root, tc.path)
			if err != nil {
				t.Fatalf("open %q: %v", tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("read %q = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestOpenLocalFileRefusesAComponentSwappedAfterResolution: the resolution is
// only the classification; the bytes still open through the root's handle, so
// a component that becomes an escaping link between the two is refused and
// never read (ADR 0047).
func TestOpenLocalFileRefusesAComponentSwappedAfterResolution(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside", "photo.png"),
		[]byte("inside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "photo.png"),
		[]byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	escape, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}

	const name = "inside/photo.png"
	rel, err := localRelativePath(root, name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	// Swap the resolved component for a link that leaves the root.
	if err := os.RemoveAll(filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escape, filepath.Join(root, "inside")); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}

	file, err := openResolved(dir, root, name, rel)
	if err == nil {
		data, _ := io.ReadAll(file)
		file.Close()
		t.Fatalf("opened %q = %q, want a refusal", name, data)
	}
	if !errors.Is(err, ErrOutsideFileRoot) {
		t.Fatalf("error %v is not a file-root refusal", err)
	}
}
