package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// commandHelp is one node of the CLI tree. Routing and help both read it, so a
// command cannot exist in one and not the other. Groups have subcommands;
// leaves carry a usage line, examples, the flags to render, and the function
// that runs them.
type commandHelp struct {
	name     string
	summary  string
	usage    string
	long     string
	examples []string
	moreHelp string
	flags    func() *flag.FlagSet
	run      func(args []string) int
	sub      []commandHelp
}

// dispatch routes args through the tree and returns the exit code. A missing or
// unknown command prints help: usage errors to stderr with exit code 2, an
// explicit help request to stdout with exit code 0.
func dispatch(node *commandHelp, path, args []string) int {
	if len(args) == 0 {
		printHelp(os.Stderr, path...)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		printHelp(os.Stdout, path...)
		return 0
	case "--version":
		// The only global flag: everything else lives on its subcommand
		// (ADR 0025).
		fmt.Printf("botflota %s\n", version)
		return 0
	}
	child := childNamed(node, args[0])
	if child == nil {
		fmt.Fprintf(os.Stderr, "unknown %s %q\n\n", subject(path), args[0])
		printHelp(os.Stderr, path...)
		return 2
	}
	childPath := append(append([]string{}, path...), child.name)
	if len(child.sub) > 0 {
		return dispatch(child, childPath, args[1:])
	}
	return child.run(args[1:])
}

func childNamed(node *commandHelp, name string) *commandHelp {
	for i := range node.sub {
		if node.sub[i].name == name {
			return &node.sub[i]
		}
	}
	return nil
}

// subject names what an unknown argument failed to match.
func subject(path []string) string {
	if len(path) == 0 {
		return "command"
	}
	return path[len(path)-1] + " subcommand"
}

func cliTree() *commandHelp {
	return &commandHelp{
		name:    "botflota",
		summary: "Declarative WhatsApp fleet runner.",
		usage:   "botflota <command> [options]",
		examples: []string{
			"botflota config validate",
			"botflota bot list",
			"botflota bot pair support-bot",
			"botflota fleet run",
			"botflota fleet status",
			"botflota chat list support-bot",
			"botflota message tail support-bot 5215500000000 --follow",
		},
		flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("botflota", flag.ContinueOnError)
			fs.Bool("version", false, "print the version and exit")
			return fs
		},
		moreHelp: "for more help on a command: botflota <command> --help",
		sub: []commandHelp{
			{
				name:    "config",
				summary: "manage configuration",
				long:    "Create a configuration, validate it, and render its JSON Schema.",
				usage:   "botflota config <command> [options]",
				examples: []string{
					"botflota config init",
					"botflota config validate",
				},
				moreHelp: "for more help: botflota config <command> --help",
				sub: []commandHelp{
					{
						name:    "init",
						summary: "create a config file and its schema",
						long: "Write a minimal config file and the generated JSON Schema\n" +
							"beside it, defaulting to the user config directory. It never\n" +
							"touches an existing file without --force.",
						usage: "botflota config init [options]",
						examples: []string{
							"botflota config init",
							"botflota config init --config etc/config.yml",
						},
						flags: func() *flag.FlagSet { return newInitCmd().fs },
						run:   runInit,
					},
					{
						name:    "validate",
						summary: "validate a config file",
						long:    "Load a config file and report every problem with its file, entity, and reason.",
						usage:   "botflota config validate [options]",
						examples: []string{
							"botflota config validate",
							"botflota config validate --config etc/config.yml",
							"botflota config validate --json",
						},
						flags: func() *flag.FlagSet { return newValidateCmd().fs },
						run:   runValidate,
					},
					{
						name:    "schema",
						summary: "print the generated config JSON Schema",
						long:    "Print the JSON Schema generated from the config types.",
						usage:   "botflota config schema [options]",
						examples: []string{
							"botflota config schema",
							"botflota config schema -o config.schema.json",
						},
						flags: func() *flag.FlagSet { return newSchemaCmd().fs },
						run:   runSchema,
					},
				},
			},
			{
				name:    "bot",
				summary: "operate on bots",
				long:    "List pairing state, pair a device, and remove a local pairing.",
				usage:   "botflota bot <command> [options]",
				examples: []string{
					"botflota bot list",
					"botflota bot pair support-bot",
					"botflota bot unpair support-bot",
				},
				moreHelp: "for more help: botflota bot <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "list configured bots and their pairing",
						long: "Print a table of configured bots with their phone number,\n" +
							"pairing state, and last change. It does not change stored state.",
						usage: "botflota bot list [options]",
						examples: []string{
							"botflota bot list",
							"botflota bot list --config etc/config.yml",
						},
						flags: func() *flag.FlagSet { return newBotListCmd().fs },
						run:   runBotList,
					},
					{
						name:    "pair",
						summary: "pair a bot by scanning a QR code",
						long: "Link a bot to WhatsApp by scanning a QR code. The command\n" +
							"rejects a phone number already owned by another bot.",
						usage: "botflota bot pair [options] <bot>",
						examples: []string{
							"botflota bot pair support-bot",
							"botflota bot pair --scope dev support-bot",
							"botflota bot pair --force support-bot",
						},
						flags: func() *flag.FlagSet { return newPairCmd().fs },
						run:   runPair,
					},
					{
						name:    "unpair",
						summary: "remove a bot's local pairing",
						long: "Remove local credentials and the account record. It does not\n" +
							"unlink the companion in WhatsApp.",
						usage: "botflota bot unpair [options] <bot>",
						examples: []string{
							"botflota bot unpair support-bot",
							"botflota bot unpair --scope dev support-bot",
						},
						flags: func() *flag.FlagSet { return newUnpairCmd().fs },
						run:   runUnpair,
					},
				},
			},
			{
				name:    "fleet",
				summary: "run and manage the fleet",
				long:    "Run the fleet of paired bots and inspect the running one.",
				usage:   "botflota fleet <command> [options]",
				examples: []string{
					"botflota fleet run",
					"botflota fleet status",
				},
				moreHelp: "for more help: botflota fleet <command> --help",
				sub: []commandHelp{
					{
						name:    "run",
						summary: "run every paired bot until interrupted",
						long: "Start every paired bot, answer inbound messages with the\n" +
							"configured pacing, and stop on interrupt after draining the outbox.",
						usage: "botflota fleet run [options]",
						examples: []string{
							"botflota fleet run",
							"botflota fleet run --config etc/config.yml",
						},
						flags: func() *flag.FlagSet { return newFleetRunCmd().fs },
						run:   runFleet,
					},
					{
						name:    "status",
						summary: "show the running fleet's state",
						long: "Ask the running instance for its uptime and each bot's\n" +
							"connection state, outbox depth, and active sessions. It reads\n" +
							"the REST API, so the fleet must be running.",
						usage: "botflota fleet status [options]",
						examples: []string{
							"botflota fleet status",
							"botflota fleet status --json",
						},
						flags: func() *flag.FlagSet { return newFleetStatusCmd().fs },
						run:   runFleetStatus,
					},
				},
			},
			{
				name:    "chat",
				summary: "list a bot's chats",
				long:    "List the stored chats of one bot through the REST API.",
				usage:   "botflota chat <command> [options]",
				examples: []string{
					"botflota chat list support-bot",
				},
				moreHelp: "for more help: botflota chat <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "list one bot's chats and their senders",
						long: "Print one row per chat with its sender's phone and name, the\n" +
							"stored message total split into sync and live, and the first\n" +
							"and last activity, newest activity first. The bot is named as\n" +
							"in bot list; add --scope when the name exists in more than one\n" +
							"scope.",
						usage: "botflota chat list [options] <bot>",
						examples: []string{
							"botflota chat list support-bot",
							"botflota chat list --limit 100 support-bot",
						},
						flags: func() *flag.FlagSet { return newChatListCmd().fs },
						run:   runChatList,
					},
				},
			},
			{
				name:    "message",
				summary: "read a chat's message history",
				long:    "Read the stored messages of one chat through the REST API.",
				usage:   "botflota message <command> [options]",
				examples: []string{
					"botflota message list support-bot 5215500000000",
					"botflota message tail support-bot 5215500000000 --follow",
				},
				moreHelp: "for more help: botflota message <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "print the newest messages of one chat",
						long: "Print the last messages exchanged with one sender, oldest\n" +
							"first. The bot is named as in bot list; add --scope when the\n" +
							"name exists in more than one scope.",
						usage: "botflota message list [options] <bot> <sender>",
						examples: []string{
							"botflota message list support-bot 5215500000000",
							"botflota message list --limit 100 support-bot 5215500000000",
						},
						flags: func() *flag.FlagSet { return newMessageCmd("message list", false).fs },
						run:   runMessageList,
					},
					{
						name:    "tail",
						summary: "follow one chat's messages",
						long: "Print the newest messages and, with --follow, keep polling\n" +
							"for new ones until interrupted.",
						usage: "botflota message tail [options] <bot> <sender>",
						examples: []string{
							"botflota message tail support-bot 5215500000000",
							"botflota message tail --follow support-bot 5215500000000",
						},
						flags: func() *flag.FlagSet { return newMessageCmd("message tail", true).fs },
						run:   runMessageTail,
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
