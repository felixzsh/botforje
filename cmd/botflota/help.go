package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

func printHelp(w io.Writer, path ...string) {
	node := findHelp(path...)
	if node == nil {
		node = cliTree()
	}
	fmt.Fprint(w, renderHelp(node))
}

func findHelp(path ...string) *commandHelp {
	node := cliTree()
	for _, part := range path {
		var next *commandHelp
		for i := range node.sub {
			if node.sub[i].name == part {
				next = &node.sub[i]
				break
			}
		}
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}

type helpRow struct {
	left  string
	right string
}

func renderHelp(node *commandHelp) string {
	var b strings.Builder
	fmt.Fprintf(&b, "usage: %s\n\n", node.usage)
	if node.long != "" {
		fmt.Fprintf(&b, "%s\n\n", node.long)
	} else if node.summary != "" {
		fmt.Fprintf(&b, "%s\n\n", node.summary)
	}
	if len(node.sub) > 0 {
		b.WriteString("commands:\n")
		rows := make([]helpRow, 0, len(node.sub))
		for i := range node.sub {
			rows = append(rows, helpRow{node.sub[i].name, node.sub[i].summary})
		}
		writeRows(&b, rows)
		b.WriteString("\n")
	}
	b.WriteString("options:\n")
	writeRows(&b, optionRows(node))
	b.WriteString("\n")
	if len(node.examples) > 0 {
		b.WriteString("examples:\n")
		for _, example := range node.examples {
			fmt.Fprintf(&b, "  %s\n", example)
		}
		b.WriteString("\n")
	}
	if node.moreHelp != "" {
		fmt.Fprintf(&b, "%s\n", node.moreHelp)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func optionRows(node *commandHelp) []helpRow {
	var rows []helpRow
	if node.flags != nil {
		node.flags().VisitAll(func(f *flag.Flag) {
			left, right := flagRow(f)
			rows = append(rows, helpRow{left, right})
		})
	}
	return append(rows, helpRow{"-h, --help", "show this help and exit"})
}

// A usage string carries its placeholder back-quoted; flag.UnquoteUsage
// returns it separately, and flagRow moves it to the left column so the flag
// name and its argument read together.
func flagRow(f *flag.Flag) (string, string) {
	name, usage := flag.UnquoteUsage(f)
	left := "--" + f.Name
	if len(f.Name) == 1 {
		left = "-" + f.Name
	}
	if name != "" {
		left += " " + name
		usage = strings.TrimSpace(strings.TrimPrefix(usage, name))
	}
	return left, usage
}

func writeRows(b *strings.Builder, rows []helpRow) {
	width := 0
	for _, row := range rows {
		if len(row.left) > width {
			width = len(row.left)
		}
	}
	for _, row := range rows {
		fmt.Fprintf(b, "  %-*s   %s\n", width, row.left, row.right)
	}
}
