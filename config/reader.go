package config

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// fileReader is the read side of a document tree. The loader always goes
// through it, so a caller that has not written its files yet can still ask the
// loader to merge and validate the tree those files would produce (ADR 0002:
// the whole candidate is validated before anything is written).
type fileReader interface {
	readFile(path string) ([]byte, error)
	open(path string) (io.ReadCloser, error)
	stat(path string) (fs.FileInfo, error)
	readDir(path string) ([]fs.DirEntry, error)
	glob(pattern string) ([]string, error)
}

func newFileReader(overlay map[string][]byte) fileReader {
	if len(overlay) == 0 {
		return osReader{}
	}
	return overlayReader{base: osReader{}, files: normalizeOverlay(overlay)}
}

type osReader struct{}

func (osReader) readFile(path string) ([]byte, error)       { return os.ReadFile(path) }
func (osReader) open(path string) (io.ReadCloser, error)    { return os.Open(path) }
func (osReader) stat(path string) (fs.FileInfo, error)      { return os.Stat(path) }
func (osReader) readDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }
func (osReader) glob(pattern string) ([]string, error)      { return filepath.Glob(pattern) }

// overlayReader answers from memory first, so a candidate tree resolves its
// includes and its content without the files being on disk yet.
type overlayReader struct {
	base  osReader
	files map[string][]byte
}

func normalizeOverlay(files map[string][]byte) map[string][]byte {
	clean := make(map[string][]byte, len(files))
	for path, content := range files {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		clean[filepath.Clean(path)] = content
	}
	return clean
}

func (r overlayReader) readFile(path string) ([]byte, error) {
	if content, ok := r.files[filepath.Clean(path)]; ok {
		return content, nil
	}
	return r.base.readFile(path)
}

// open hands out a stream at the content, so a caller that bounds what it reads
// reads exactly as much from memory as it would from disk.
func (r overlayReader) open(path string) (io.ReadCloser, error) {
	if content, ok := r.files[filepath.Clean(path)]; ok {
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	return r.base.open(path)
}

func (r overlayReader) stat(path string) (fs.FileInfo, error) {
	clean := filepath.Clean(path)
	content, ok := r.files[clean]
	if !ok {
		return r.base.stat(path)
	}
	return overlayInfo{name: filepath.Base(clean), size: int64(len(content))}, nil
}

func (r overlayReader) readDir(path string) ([]fs.DirEntry, error) {
	clean := filepath.Clean(path)
	var entries []fs.DirEntry
	for file, content := range r.files {
		if filepath.Dir(file) == clean {
			entries = append(entries, overlayEntry{
				info: overlayInfo{name: filepath.Base(file), size: int64(len(content))},
			})
		}
	}
	real, err := r.base.readDir(clean)
	if err != nil {
		if len(entries) == 0 {
			return nil, err
		}
	} else {
		entries = append(entries, real...)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries, nil
}

func (r overlayReader) glob(pattern string) ([]string, error) {
	matches, err := r.base.glob(pattern)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(matches))
	for _, match := range matches {
		known[filepath.Clean(match)] = true
	}
	for file := range r.files {
		if known[file] {
			continue
		}
		if ok, _ := filepath.Match(pattern, file); ok {
			matches = append(matches, file)
		}
	}
	return matches, nil
}

type overlayEntry struct{ info fs.FileInfo }

func (e overlayEntry) Name() string               { return e.info.Name() }
func (e overlayEntry) IsDir() bool                { return false }
func (e overlayEntry) Type() fs.FileMode          { return 0 }
func (e overlayEntry) Info() (fs.FileInfo, error) { return e.info, nil }

// overlayInfo describes a file that exists only in memory. It is never a
// directory: an overlay holds document content, not trees.
type overlayInfo struct {
	name string
	size int64
}

func (i overlayInfo) Name() string       { return i.name }
func (i overlayInfo) Size() int64        { return i.size }
func (i overlayInfo) Mode() fs.FileMode  { return 0o644 }
func (i overlayInfo) ModTime() time.Time { return time.Time{} }
func (i overlayInfo) IsDir() bool        { return false }
func (i overlayInfo) Sys() any           { return nil }
