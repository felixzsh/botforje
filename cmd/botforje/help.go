package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// commandHelp is one node of the CLI help tree. Groups have subcommands;
// leaves carry a usage line, examples, and the flags to render. Keep it in
// sync with the routers in main.go.
type commandHelp struct {
	name     string
	summary  string
	usage    string
	long     string
	examples []string
	moreHelp string
	flags    func() *flag.FlagSet
	sub      []commandHelp
}

func cliTree() *commandHelp {
	return &commandHelp{
		name:    "botforje",
		summary: "Declarative WhatsApp fleet runner.",
		usage:   "botforje <command> [options]",
		examples: []string{
			"botforje config validate",
			"botforje bot list",
			"botforje bot pair support-bot",
			"botforje daemon run",
		},
		moreHelp: "for more help on a command: botforje <command> --help",
		sub: []commandHelp{
			{
				name:    "config",
				summary: "manage configuration",
				long:    "Validate the configuration and render its JSON Schema.",
				usage:   "botforje config <command> [options]",
				examples: []string{
					"botforje config validate",
					"botforje config schema",
				},
				moreHelp: "for more help: botforje config <command> --help",
				sub: []commandHelp{
					{
						name:    "validate",
						summary: "validate a config file",
						long:    "Load a config file and report every problem with its file, entity, and reason.",
						usage:   "botforje config validate [options]",
						examples: []string{
							"botforje config validate",
							"botforje config validate --config etc/config.yml",
							"botforje config validate --json",
						},
						flags: func() *flag.FlagSet { return newValidateCmd().fs },
					},
					{
						name:    "schema",
						summary: "print the generated config JSON Schema",
						long:    "Print the JSON Schema generated from the config types.",
						usage:   "botforje config schema [options]",
						examples: []string{
							"botforje config schema",
							"botforje config schema -o config.schema.json",
						},
						flags: func() *flag.FlagSet { return newSchemaCmd().fs },
					},
				},
			},
			{
				name:    "bot",
				summary: "operate on bots",
				long:    "List pairing state, pair a device, and remove a local pairing.",
				usage:   "botforje bot <command> [options]",
				examples: []string{
					"botforje bot list",
					"botforje bot pair support-bot",
					"botforje bot unpair support-bot",
				},
				moreHelp: "for more help: botforje bot <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "list configured bots and their pairing",
						long: "Print a table of configured bots with their phone number,\n" +
							"pairing state, and last change. It does not change stored state.",
						usage: "botforje bot list [options]",
						examples: []string{
							"botforje bot list",
							"botforje bot list --config etc/config.yml",
						},
						flags: func() *flag.FlagSet { return newBotListCmd().fs },
					},
					{
						name:    "pair",
						summary: "pair a bot by scanning a QR code",
						long: "Link a bot to WhatsApp by scanning a QR code. The command\n" +
							"rejects a phone number already owned by another bot.",
						usage: "botforje bot pair [options] <bot>",
						examples: []string{
							"botforje bot pair support-bot",
							"botforje bot pair --scope dev support-bot",
							"botforje bot pair --force support-bot",
						},
						flags: func() *flag.FlagSet { return newPairCmd().fs },
					},
					{
						name:    "unpair",
						summary: "remove a bot's local pairing",
						long: "Remove local credentials and the account record. It does not\n" +
							"unlink the companion in WhatsApp.",
						usage: "botforje bot unpair [options] <bot>",
						examples: []string{
							"botforje bot unpair support-bot",
							"botforje bot unpair --scope dev support-bot",
						},
						flags: func() *flag.FlagSet { return newUnpairCmd().fs },
					},
				},
			},
			{
				name:    "daemon",
				summary: "run and manage the daemon",
				long:    "Run the fleet and, in a later phase, inspect and control the running daemon.",
				usage:   "botforje daemon <command> [options]",
				examples: []string{
					"botforje daemon run",
				},
				moreHelp: "for more help: botforje daemon <command> --help",
				sub: []commandHelp{
					{
						name:    "run",
						summary: "run every paired bot until interrupted",
						long: "Start every paired bot, answer inbound messages with the\n" +
							"configured pacing, and stop on interrupt after draining the outbox.",
						usage: "botforje daemon run [options]",
						examples: []string{
							"botforje daemon run",
							"botforje daemon run --config etc/config.yml",
						},
						flags: func() *flag.FlagSet { return newDaemonRunCmd().fs },
					},
				},
			},
		},
	}
}

// printHelp writes the help for the command at path to w. An unknown path
// falls back to the root help.
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

// helpRow is one aligned entry in a commands or options section.
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

// flagRow renders one flag as "--name PLACEHOLDER" plus its description. A
// usage string starts with a back-quoted placeholder, which moves to the left
// column so the flag name and its argument read together.
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

// parseArgs parses args and reports an exit code when parsing stops: 0 after
// an explicit help request, 2 on a usage error, and -1 to continue.
func parseArgs(fs *flag.FlagSet, args []string) int {
	err := fs.Parse(args)
	if err == nil {
		return -1
	}
	path := strings.Fields(fs.Name())
	if errors.Is(err, flag.ErrHelp) {
		printHelp(os.Stdout, path...)
		return 0
	}
	printHelp(os.Stderr, path...)
	return 2
}
