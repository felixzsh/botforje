//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || ios || android

package instance

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock without waiting for it; the
// operating system drops it when the file descriptor closes.
func lockFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("instance: open lock file: %w", err)
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return file, nil
	}
	if closeErr := file.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, ErrOwned
	}
	return nil, fmt.Errorf("instance: lock %s: %w", path, err)
}

func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
