// Package instance makes one process the owner of one instance's data
// directory (ADR 0039).
package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the advisory lock file, kept in data_dir beside the database.
const FileName = "instance.lock"

// ErrOwned reports that another process already owns the instance. The lock
// belongs to a live process, so the error can never be stale.
var ErrOwned = errors.New("another botflota process owns this instance")

// Lock is the exclusive ownership of one instance for the life of the
// process.
type Lock struct {
	file *os.File
}

// Acquire takes the instance without waiting; a busy one answers ErrOwned.
// Release gives it back, and so does the process exiting.
func Acquire(dataDir string) (*Lock, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("instance: create data dir: %w", err)
	}
	file, err := lockFile(filepath.Join(dataDir, FileName))
	if err != nil {
		return nil, err
	}
	return &Lock{file: file}, nil
}

// Release gives the instance back; a nil Lock or a second call is a no-op.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockFile(l.file)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.file = nil
	if err != nil {
		return fmt.Errorf("instance: release: %w", err)
	}
	return nil
}
