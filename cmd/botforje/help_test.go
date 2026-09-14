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

func TestHelpOutputSections(t *testing.T) {
	cases := []struct {
		name     string
		path     []string
		contains []string
	}{
		{
			name: "root",
			contains: []string{
				"usage: botforje", "commands:", "options:", "examples:",
				"for more help",
			},
		},
		{
			name: "config",
			path: []string{"config"},
			contains: []string{
				"usage: botforje config", "validate", "schema", "options:",
			},
		},
		{
			name: "bot",
			path: []string{"bot"},
			contains: []string{
				"usage: botforje bot", "list", "pair", "unpair",
			},
		},
		{
			name: "pair",
			path: []string{"bot", "pair"},
			contains: []string{
				"usage: botforje bot pair", "options:", "--config", "--scope",
				"--force", "-h, --help", "<bot>",
			},
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
