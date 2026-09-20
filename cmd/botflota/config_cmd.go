package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/felixzsh/botflota/config"
)

// schemaFileName is the generated schema written beside the config file.
const schemaFileName = "config.schema.json"

// minimalConfig is the starting point config init writes: a valid document
// with nothing to delete.
var minimalConfig = []byte(`# Botflota configuration. Validate it with botflota config validate.
# Secrets stay out of this file: a model api_key or a request header may
# reference an environment variable as ${ENV_VAR} (ADR 0014).
$schema: "config.schema.json"
version: 1

# A scope isolates its bots, graphs, actions, and model profiles; one scope
# per audience, product, or environment.
scopes: {}
`)

type initCmd struct {
	fs     *flag.FlagSet
	config *string
	force  *bool
}

func newInitCmd() *initCmd {
	fs := flag.NewFlagSet("config init", flag.ContinueOnError)
	fs.Usage = func() {}
	return &initCmd{
		fs:     fs,
		config: configFlag(fs),
		force:  fs.Bool("force", false, "overwrite an existing config or schema"),
	}
}

// runInit writes a minimal config file and its generated schema beside it.
// It is non-interactive, refuses to touch an existing file without --force,
// and needs no running instance (ADR 0025).
func runInit(args []string) int {
	cmd := newInitCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	path := *cmd.config
	if path == "" {
		found, err := defaultConfigPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		path = found
	}
	if err := initConfig(path, *cmd.force); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("wrote %s and %s\n",
		path, filepath.Join(filepath.Dir(path), schemaFileName))
	return 0
}

// initConfig writes the minimal config and the generated schema.
func initConfig(path string, force bool) error {
	schemaPath := filepath.Join(filepath.Dir(path), schemaFileName)
	if !force {
		for _, existing := range []string{path, schemaPath} {
			if _, err := os.Stat(existing); err == nil {
				return fmt.Errorf("%s exists; use --force to overwrite", existing)
			}
		}
	}
	schema, err := config.JSONSchema()
	if err != nil {
		return fmt.Errorf("generate schema: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, minimalConfig, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(schemaPath, schema, 0o644); err != nil {
		return err
	}
	return nil
}

type validateCmd struct {
	fs     *flag.FlagSet
	config *string
	json   *bool
}

func newValidateCmd() *validateCmd {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	fs.Usage = func() {}
	return &validateCmd{
		fs:     fs,
		config: configFlag(fs),
		json:   fs.Bool("json", false, "machine-readable output"),
	}
}

type schemaCmd struct {
	fs  *flag.FlagSet
	out *string
}

func newSchemaCmd() *schemaCmd {
	fs := flag.NewFlagSet("config schema", flag.ContinueOnError)
	fs.Usage = func() {}
	return &schemaCmd{
		fs:  fs,
		out: fs.String("o", "", "`FILE` output file (default: stdout)"),
	}
}

func runValidate(args []string) int {
	cmd := newValidateCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}

	path := *cmd.config
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
		if *cmd.json {
			printJSON([]config.ValidationError{{File: path, Reason: err.Error()}})
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		return 1
	}

	if errs := config.Validate(cfg); len(errs) > 0 {
		if *cmd.json {
			printJSON(errs)
		} else {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, e.Error())
			}
			fmt.Fprintf(os.Stderr, "\n%d problem(s) in %s\n", len(errs), path)
		}
		return 1
	}

	if *cmd.json {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": true, "config": path})
	} else {
		fmt.Printf("OK: %s\n", path)
	}
	return 0
}

func runSchema(args []string) int {
	cmd := newSchemaCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	data, err := config.JSONSchema()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *cmd.out == "" {
		_, _ = os.Stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(*cmd.out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("wrote %s\n", *cmd.out)
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
