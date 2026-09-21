package main

import (
	"bytes"
	"flag"
	"os"
	"slices"
	"strings"
	"testing"
)

func walkHelp(node *commandHelp, visit func(*commandHelp)) {
	visit(node)
	for i := range node.sub {
		walkHelp(&node.sub[i], visit)
	}
}

func TestHelpTreeIsComplete(t *testing.T) {
	count := 0
	walkHelp(cliTree(), func(node *commandHelp) {
		count++
		if node.name == "" || node.usage == "" {
			t.Errorf("node %q: name and usage are required", node.name)
		}
		if node.summary == "" && node.long == "" {
			t.Errorf("node %q: needs a summary or long description", node.name)
		}
		if len(node.sub) == 0 && node.flags == nil {
			t.Errorf("leaf %q: needs flags", node.name)
		}
		if len(node.sub) == 0 && node.run == nil {
			t.Errorf("leaf %q: needs a run function", node.name)
		}
	})
	if count < 8 {
		t.Fatalf("tree has %d nodes, want at least 8", count)
	}
}

// TestHelpOutputSections checks the help's shape, not its wording: command
// names and flags are deliberately not asserted.
func TestHelpOutputSections(t *testing.T) {
	cases := []struct {
		name     string
		path     []string
		contains []string
		absent   []string
	}{
		{
			name: "root",
			contains: []string{
				"usage: botflota", "commands:", "options:", "examples:",
				"for more help",
			},
		},
		{
			name:     "config group",
			path:     []string{"config"},
			contains: []string{"usage: botflota config", "commands:", "options:"},
		},
		{
			name:     "bot group",
			path:     []string{"bot"},
			contains: []string{"usage: botflota bot", "commands:", "options:"},
		},
		{
			name:     "pair leaf",
			path:     []string{"bot", "pair"},
			contains: []string{"usage: botflota bot pair", "options:", "-h, --help"},
			absent:   []string{"commands:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buffer bytes.Buffer
			printHelp(&buffer, tc.path...)
			out := buffer.String()
			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Errorf("help %v is missing %q:\n%s", tc.path, want, out)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(out, unwanted) {
					t.Errorf("help %v should not contain %q:\n%s", tc.path, unwanted, out)
				}
			}
		})
	}
}

// TestOrderFlags covers interspersed flags: one written after the arguments
// must not be dropped in silence.
func TestOrderFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "boolean after the arguments",
			args: []string{"dev-bot", "--force"},
			want: []string{"--force", "dev-bot"},
		},
		{
			name: "value stays with its flag",
			args: []string{"dev-bot", "5215", "--count", "25"},
			want: []string{"--count", "25", "dev-bot", "5215"},
		},
		{
			name: "boolean does not eat the next argument",
			args: []string{"--force", "dev-bot"},
			want: []string{"--force", "dev-bot"},
		},
		{
			name: "inline value",
			args: []string{"dev-bot", "--count=25"},
			want: []string{"--count=25", "dev-bot"},
		},
		{
			name: "terminator ends the flags",
			args: []string{"dev-bot", "--", "--force"},
			want: []string{"dev-bot", "--", "--force"},
		},
		{
			name: "a lone dash is an argument",
			args: []string{"dev-bot", "-"},
			want: []string{"dev-bot", "-"},
		},
		{
			name: "help is not a value flag",
			args: []string{"dev-bot", "-h"},
			want: []string{"-h", "dev-bot"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.Bool("force", false, "")
			fs.Int("count", 0, "")
			got := orderFlags(fs, tc.args)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("orderFlags(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseArgsAcceptsAFlagAfterTheArguments(t *testing.T) {
	pair := newPairCmd()
	if code := parseArgs(pair.fs, []string{"dev-bot", "--import-history"}); code != -1 {
		t.Fatalf("parseArgs = %d, want -1", code)
	}
	if !*pair.importHistory {
		t.Fatal("a flag after the arguments was dropped")
	}
	if pair.fs.NArg() != 1 || pair.fs.Arg(0) != "dev-bot" {
		t.Fatalf("arguments = %v, want just the bot", pair.fs.Args())
	}
}

func TestParseArgsRejectsAnUnknownFlag(t *testing.T) {
	silenceStderr(t)
	pair := newPairCmd()
	if code := parseArgs(pair.fs, []string{"dev-bot", "--import-histoy"}); code != 2 {
		t.Fatalf("parseArgs = %d, want 2", code)
	}
}

// silenceStderr works because printHelp reads os.Stderr at call time.
func silenceStderr(t *testing.T) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	previous := os.Stderr
	os.Stderr = file
	t.Cleanup(func() {
		os.Stderr = previous
		_ = file.Close()
	})
}

func TestHelpLinesWithinLimit(t *testing.T) {
	walkHelp(cliTree(), func(node *commandHelp) {
		out := strings.TrimRight(renderHelp(node), "\n")
		for i, line := range strings.Split(out, "\n") {
			if len(line) > 100 {
				t.Errorf("%s line %d is %d columns: %q", node.name, i+1, len(line), line)
			}
		}
	})
}
