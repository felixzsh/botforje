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
	if path == "" {
		return nil, errors.New("local file path is empty")
	}
	if filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: %q is absolute; use a path relative to %q",
			ErrOutsideFileRoot, path, root)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("file root %q: %w", root, err)
	}
	defer dir.Close()
	if err := checkInsideRoot(root, path); err != nil {
		return nil, err
	}
	file, err := dir.Open(filepath.FromSlash(path))
	if err != nil {
		return nil, fmt.Errorf("open %q under file root %q: %w", path, root, err)
	}
	return file, nil
}

// checkInsideRoot resolves path under root, following symbolic links, so an
// escape is reported as one. The open itself still goes through os.Root, which
// refuses a link swapped in after this check.
func checkInsideRoot(root, path string) error {
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("file root %q: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(base, path))
	if err != nil {
		return fmt.Errorf("open %q under file root %q: %w", path, root, err)
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil {
		// Roots on unrelated volumes cannot be compared; the open through
		// os.Root still confines the read.
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %q resolves outside %q", ErrOutsideFileRoot, path, root)
	}
	return nil
}
