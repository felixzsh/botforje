// Command botforje is the BotForje CLI. Phase 1 implements validate and the
// schema generator; the rest of the commands arrive in later phases.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/felixzsh/botforje/internal/config"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "validate":
		os.Exit(runValidate(os.Args[2:]))
	case "schema":
		os.Exit(runSchema(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `botforje <command>

Commands:
  validate   validate a config file (--config, --json)
  schema     print the generated config JSON Schema (-o file)
`)
}

func runValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to config.yml (default: XDG discovery)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path := *configPath
	if path == "" {
		found, err := discoverConfig()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		path = found
	}

	cfg, err := (config.Loader{}).Load(path)
	if err != nil {
		if *asJSON {
			printJSON([]config.ValidationError{{File: path, Reason: err.Error()}})
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}

	if errs := config.Validate(cfg); len(errs) > 0 {
		if *asJSON {
			printJSON(errs)
		} else {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, e.Error())
			}
			fmt.Fprintf(os.Stderr, "\n%d problem(s) in %s\n", len(errs), path)
		}
		return 1
	}

	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": true, "config": path})
	} else {
		fmt.Printf("OK: %s\n", path)
	}
	return 0
}

func runSchema(args []string) int {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	out := fs.String("o", "", "output file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	data, err := config.JSONSchema()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *out == "" {
		_, _ = os.Stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("wrote %s\n", *out)
	return 0
}

func printJSON(errs []config.ValidationError) {
	type item struct {
		File   string `json:"file,omitempty"`
		Entity string `json:"entity,omitempty"`
		Reason string `json:"reason"`
	}
	items := make([]item, 0, len(errs))
	for _, e := range errs {
		items = append(items, item{File: e.File, Entity: e.Entity, Reason: e.Reason})
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": false, "errors": items})
}

func discoverConfig() (string, error) {
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "botforje", "config.yml"))
	}
	candidates = append(candidates, "/etc/botforje/config.yml")
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("no config found; tried %s (use --config)",
		strings.Join(candidates, ", "))
}
