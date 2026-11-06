package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// secretCatalog holds one scope's secret names as the owner's file holds them
// (ADR 0049). It is data only: no includes, no command substitution.
type secretCatalog map[string]string

// lookup resolves one name from the catalog. A name it does not hold is
// missing, with no fallback to the process environment.
func (c secretCatalog) lookup(name string) (string, bool) {
	value, held := c[name]
	return value, held
}

// parseSecretCatalog reads `NAME=value` lines, blank lines, and `#` comments.
// The value is the rest of the line as written, so a second `=` belongs to it;
// a later duplicate name wins, as a shell environment would. An error never
// quotes a value, only a line number.
func parseSecretCatalog(data []byte) (secretCatalog, error) {
	catalog := secretCatalog{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("line %d: want NAME=value", i+1)
		}
		catalog[name] = value
	}
	return catalog, nil
}

// readSecretCatalogs reads one catalog per scope that the main config file
// declares (ADR 0049). A relative path is relative to the main config file, and
// the file must sit outside every file root, resolved, so a catalog can never
// be sent as media (ADR 0047).
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
		data, err := files.readFile(path)
		if err != nil {
			return nil, fmt.Errorf("secret_files[%q]: %w", scope, err)
		}
		catalog, err := parseSecretCatalog(data)
		if err != nil {
			return nil, fmt.Errorf("secret_files[%q]: %s: %w", scope, path, err)
		}
		if err := checkCatalogOutsideRoots(scope, path, roots); err != nil {
			return nil, err
		}
		catalogs[scope] = catalog
	}
	return catalogs, nil
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
// ADR 0049).
func checkCatalogOutsideRoots(scope, path string, roots map[string]string) error {
	real := resolvedPath(path)
	for _, name := range SortedKeys(roots) {
		if containsDirectory(roots[name], real) {
			return fmt.Errorf(
				"secret_files[%q]: %q is inside file_roots[%q] (%q); keep it outside",
				scope, path, name, roots[name])
		}
	}
	return nil
}

// resolvedPath follows symbolic links when it can, so a link from outside a
// root into it is caught too. A path the filesystem does not resolve yet is
// compared as it is.
func resolvedPath(path string) string {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return real
}
