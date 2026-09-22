//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || ios || android)

package instance

import (
	"errors"
	"os"
)

// lockFile refuses instead of pretending: without file locking this process
// cannot be the proven owner of an instance (ADR 0039).
func lockFile(string) (*os.File, error) {
	return nil, errors.New(
		"instance: no file locking on this platform, so an instance cannot be owned")
}

func unlockFile(*os.File) error { return nil }
