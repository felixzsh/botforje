package config

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

// maxSecretCatalogSize bounds a catalog read. A catalog holds names and short
// credentials, not data, so a larger file is a mistake and load must not read it
// whole; the same guard the media paths use.
const maxSecretCatalogSize = 1 << 20

// secretCatalog holds one scope's secret names as the owner's file holds them
// (ADR 0049). It is data only: no includes, no command substitution.
type secretCatalog map[string]string

// lookup resolves one name from the catalog. A name it does not hold is
// missing, with no fallback to the process environment.
func (c secretCatalog) lookup(name string) (string, bool) {
	value, held := c[name]
	return value, held
}

// parseSecretCatalog reads `NAME=value` lines, blank lines, and `#` comments. A
// line with no `=`, an empty name, an empty value, or a repeated name is an
// error naming the line and the name; no error quotes a value, and a value is
// otherwise the rest of its line as written, so a second `=` belongs to it.
func parseSecretCatalog(data []byte) (secretCatalog, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	catalog := secretCatalog{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		rawName, value, ok := strings.Cut(line, "=")
		name := strings.TrimSpace(rawName)
		if !ok {
			return nil, fmt.Errorf("line %d: want NAME=value", i+1)
		}
		if name == "" || value == "" {
			return nil, fmt.Errorf("line %d: name %q: want a non-empty value",
				i+1, name)
		}
		if _, duplicate := catalog[name]; duplicate {
			return nil, fmt.Errorf("line %d: duplicate name %q", i+1, name)
		}
		catalog[name] = value
	}
	return catalog, nil
}

// readSecretCatalogs reads one catalog per scope that the main config file
// declares (ADR 0049). A relative path is relative to the main config file, and
// the file must sit outside every file root, so a catalog can never be sent as
// media (ADR 0047). Containment is decided on the resolved path before anything
// is read, and a file that is not a regular, bounded file is refused unread.
func readSecretCatalogs(
	cfg *Config, mainPath string, roots map[string]string, files fileReader,
) (map[string]secretCatalog, error) {
	if len(cfg.SecretFiles) == 0 {
		return nil, nil
	}
	catalogs := make(map[string]secretCatalog, len(cfg.SecretFiles))
	for _, scope := range SortedKeys(cfg.SecretFiles) {
		path, err := catalogPath(cfg.SecretFiles[scope], mainPath)
		if err != nil {
			return nil, fmt.Errorf("secret_files[%q]: %w", scope, err)
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("secret_files[%q]: %w", scope, err)
		}
		if err := checkCatalogOutsideRoots(scope, path, real, roots); err != nil {
			return nil, err
		}
		if err := checkCatalogFile(scope, path, real, files); err != nil {
			return nil, err
		}
		data, err := files.readFile(real)
		if err != nil {
			return nil, fmt.Errorf("secret_files[%q]: %w", scope, err)
		}
		// The stat above bounds the declared file; this bounds what was actually
		// handed over, so a file that grew in between is refused too.
		if int64(len(data)) > maxSecretCatalogSize {
			return nil, catalogSizeError(scope, path)
		}
		catalog, err := parseSecretCatalog(data)
		if err != nil {
			return nil, fmt.Errorf("secret_files[%q]: %s: %w", scope, path, err)
		}
		catalogs[scope] = catalog
	}
	return catalogs, nil
}

func catalogSizeError(scope, path string) error {
	return fmt.Errorf("secret_files[%q]: %q is larger than %d bytes",
		scope, path, maxSecretCatalogSize)
}

// catalogPath resolves a declared catalog to an absolute path, relative to the
// directory of the main config file when it is not absolute.
func catalogPath(declared, mainPath string) (string, error) {
	if declared == "" {
		return "", fmt.Errorf("an empty path names no catalog")
	}
	if strings.Contains(declared, "${") {
		// A catalog path is never interpolated, so a reference here could only
		// be resolved as a literal path (ADR 0014).
		return "", fmt.Errorf("%q: a secret reference is only allowed in a "+
			"model api_key and request header values", declared)
	}
	if filepath.IsAbs(declared) {
		return filepath.Clean(declared), nil
	}
	return filepath.Join(filepath.Dir(mainPath), declared), nil
}

// checkCatalogOutsideRoots refuses a catalog inside any scope's file root: the
// root is where a media step may read from, so a catalog there could be sent as
// a document, and the whole scope's secret names would leave (ADR 0047,
// ADR 0049). real is the path with its symbolic links resolved, so a link from
// outside a root into it is caught too.
func checkCatalogOutsideRoots(scope, path, real string, roots map[string]string) error {
	for _, name := range SortedKeys(roots) {
		if containsDirectory(roots[name], real) {
			return fmt.Errorf(
				"secret_files[%q]: %q is inside file_roots[%q] (%q); keep it outside",
				scope, path, name, roots[name])
		}
	}
	return nil
}

// checkCatalogFile refuses anything but a regular file of bounded size before
// its content is read: a FIFO or a device path would block or stream the load.
func checkCatalogFile(scope, path, real string, files fileReader) error {
	info, err := files.stat(real)
	if err != nil {
		return fmt.Errorf("secret_files[%q]: %w", scope, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("secret_files[%q]: %q is not a regular file", scope, path)
	}
	if info.Size() > maxSecretCatalogSize {
		return catalogSizeError(scope, path)
	}
	return nil
}
