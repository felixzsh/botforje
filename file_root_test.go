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
