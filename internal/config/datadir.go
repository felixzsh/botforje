package config

import (
	"os"
	"path/filepath"
	"strings"
)

// DataDirPath resolves the instance data directory. An absolute data_dir is
// used as-is; a relative one resolves against the directory of the config
// file, like include paths (ADR 0016); an empty one falls back to the XDG data
// home for botforje.
func (c *Config) DataDirPath() (string, error) {
	if c.DataDir == "" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "botforje"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "botforje"), nil
	}
	dir, err := expandHome(c.DataDir)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(dir) || c.source == "" {
		return dir, nil
	}
	return filepath.Join(filepath.Dir(c.source), dir), nil
}

func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}
