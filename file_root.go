package botflota

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideFileRoot marks a local path that a scope's file root refuses: an
// absolute path, one that climbs above the root with "..", or one that a
// symbolic link points outside of (ADR 0047).
var ErrOutsideFileRoot = errors.New("local file outside the scope's file root")

// OpenLocalFile opens the local file a definition named. root is the scope's
// owner-set file root: when it is empty the path opens as written, under the
// process's own file permissions. When it is set the path must be relative, and
// the file opens through the root, so neither ".." nor a symbolic link can
// leave it (ADR 0047).
//
// The check happens here, when the bytes are opened, after templates resolved:
// a reader never trusts a prefix a caller computed earlier, and a refusal is
// returned instead of falling back to an unconfined read.
func OpenLocalFile(root, path string) (*os.File, error) {
	if root == "" {
		return os.Open(path)
	}
	rel, err := localRelativePath(root, path)
	if err != nil {
		return nil, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("file root %q: %w", root, err)
	}
	defer dir.Close()
	return openResolved(dir, root, path, rel)
}

// localRelativePath resolves path under root, following symbolic links, and
// returns it relative to the root's resolved directory. Resolving first keeps
// the open free of symbolic-link components: os.Root refuses a link whose
// target is absolute even when that target stays inside the root, and such a
// link is legitimate here (ADR 0047).
func localRelativePath(root, path string) (string, error) {
	if path == "" {
		return "", errors.New("local file path is empty")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is absolute; use a path relative to %q",
			ErrOutsideFileRoot, path, root)
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("file root %q: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(path)))
	if err != nil {
		return "", fmt.Errorf("open %q under file root %q: %w", path, root, err)
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q resolves outside %q", ErrOutsideFileRoot, path, root)
	}
	return rel, nil
}

// openResolved opens a path localRelativePath already resolved, through the
// root's own directory handle. os.Root still refuses a component that a link
// swapped for an escape after the resolution, and that refusal is reported as
// an escape so a deterministic refusal is never retried as a transient one.
func openResolved(dir *os.Root, root, name, rel string) (*os.File, error) {
	file, err := dir.Open(filepath.FromSlash(rel))
	if err != nil {
		if _, resolveErr := localRelativePath(root, name); errors.Is(
			resolveErr, ErrOutsideFileRoot) {
			return nil, fmt.Errorf("%w: %q resolves outside %q", ErrOutsideFileRoot, name, root)
		}
		return nil, fmt.Errorf("open %q under file root %q: %w", name, root, err)
	}
	return file, nil
}
