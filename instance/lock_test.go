package instance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireIsExclusive(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	second, err := Acquire(dir)
	if !errors.Is(err, ErrOwned) {
		t.Fatalf("second acquire = %v, want ErrOwned", err)
	}
	if second != nil {
		t.Fatalf("second acquire = %#v, want nil", second)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	third, err := Acquire(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("second release = %v, want a no-op", err)
	}
}

func TestAcquireCreatesDataDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "data")
	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, FileName)); err != nil {
		t.Fatalf("lock file: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}
