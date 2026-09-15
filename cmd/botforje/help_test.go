package main

import (
	"bytes"
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
	})
	if count < 8 {
		t.Fatalf("tree has %d nodes, want at least 8", count)
	}
}

// TestHelpOutputSections checks the shape of the rendered help: every node has
// a usage line and an options section, groups list commands, and leaves do not.
// Command names and flags are deliberately not asserted, so rewording them
// does not break the test.
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
				"usage: botforje", "commands:", "options:", "examples:",
				"for more help",
			},
		},
		{
			name:     "config group",
			path:     []string{"config"},
			contains: []string{"usage: botforje config", "commands:", "options:"},
		},
		{
			name:     "bot group",
			path:     []string{"bot"},
			contains: []string{"usage: botforje bot", "commands:", "options:"},
		},
		{
			name:     "pair leaf",
			path:     []string{"bot", "pair"},
			contains: []string{"usage: botforje bot pair", "options:", "-h, --help"},
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
