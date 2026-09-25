package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DataDirPath resolves the instance data directory: an absolute data_dir as
// is, a relative one against the config file like includes (ADR 0016), an
// empty one against the XDG data home.
func (c *Config) DataDirPath() (string, error) {
	return resolveDataDir(c.DataDir, c.source)
}

// LockDir resolves where an instance's lock lives straight from the main file's
// top-level data_dir, without validating the rest of the document. A process
// takes the lock with it before loading a tree that an interrupted publication
// may still be replacing, so two processes never recover the same publication
// at once (ADR 0039).
func LockDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", abs, err)
	}
	var probe struct {
		DataDir string `yaml:"data_dir"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return "", fmt.Errorf("%s: %w", abs, err)
	}
	return resolveDataDir(probe.DataDir, abs)
}

func resolveDataDir(dataDir, source string) (string, error) {
	if dataDir == "" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "botflota"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "botflota"), nil
	}
	dir, err := expandHome(dataDir)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(dir) || source == "" {
		return dir, nil
	}
	return filepath.Join(filepath.Dir(source), dir), nil
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
