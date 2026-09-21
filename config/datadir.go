package config

import (
	"os"
	"path/filepath"
	"strings"
)

// DataDirPath resolves the instance data directory: an absolute data_dir as
// is, a relative one against the config file like includes (ADR 0016), an
// empty one against the XDG data home.
func (c *Config) DataDirPath() (string, error) {
	if c.DataDir == "" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "botflota"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "botflota"), nil
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
