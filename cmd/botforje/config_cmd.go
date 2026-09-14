package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/felixzsh/botforje/internal/config"
)

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
