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
			"botflota contact list support-bot",
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
						long:    "Print the JSON Schema of the config format.",
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
							"rejects a phone number already owned by another bot, and refuses\n" +
							"to run while the API answers, because one device cannot be\n" +
							"connected twice. An already paired bot is reported and left\n" +
							"alone; --force discards that pairing and registers again, which\n" +
							"is also the only way to change what the registration carries.\n" +
							"Whatever WhatsApp delivers while the pairing connection is alive\n" +
							"is stored; --import-history also asks for the full history window\n" +
							"and waits for it, which is what brings the older chats. Without\n" +
							"it the default recent window arrives and `chat sync` pages\n" +
							"older messages per chat.\n" +
							"The command answers with what it stored: the chats and messages\n" +
							"the history sync brought, and the contacts the address book\n" +
							"added. --verbose prints the structured log at the configured\n" +
							"log_level instead of staying quiet.",
						usage: "botflota bot pair [options] <bot>",
						examples: []string{
							"botflota bot pair support-bot",
							"botflota bot pair --import-history support-bot",
							"botflota bot pair --scope dev support-bot",
							"botflota bot pair --force support-bot",
							"botflota bot pair --import-history --verbose support-bot",
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
				summary: "list a bot's chats and sync older history",
				long:    "List the stored chats of one bot and ask the phone for older history.",
				usage:   "botflota chat <command> [options]",
				examples: []string{
					"botflota chat list support-bot",
					"botflota chat sync support-bot 5215500000000",
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
					{
						name:    "sync",
						summary: "ask the phone for older messages of one chat",
						long: "Ask the primary device for older messages and import batches\n" +
							"until the cursor stops moving. --count limits the total; zero\n" +
							"means all accessible history. The running fleet owns the live\n" +
							"connection, so this is an API client and fleet run must be up.",
						usage: "botflota chat sync [options] <bot> <sender>",
						examples: []string{
							"botflota chat sync support-bot 5215500000000",
							"botflota chat sync --count 75 support-bot 5215500000000",
						},
						flags: func() *flag.FlagSet { return newChatSyncCmd().fs },
						run:   runChatSync,
					},
				},
			},
			{
				name:    "contact",
				summary: "list the address book a bot imported",
				long: "List the contacts Botflota copied from one bot's linked\n" +
					"account. A contact is an identity the account knows, whether or\n" +
					"not it ever wrote; it is not a chat. Only a bot that sets\n" +
					"settings.import_contacts has any.",
				usage: "botflota contact <command> [options]",
				examples: []string{
					"botflota contact list support-bot",
				},
				moreHelp: "for more help: botflota contact <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "print one bot's imported contacts",
						long: "Print one row per contact, most recently updated first, with\n" +
							"the address-book name, the push name, and the business name.\n" +
							"The bot is named as in bot list; add --scope when the name\n" +
							"exists in more than one scope.",
						usage: "botflota contact list [options] <bot>",
						examples: []string{
							"botflota contact list support-bot",
							"botflota contact list --limit 500 support-bot",
						},
						flags: func() *flag.FlagSet { return newContactListCmd().fs },
						run:   runContactList,
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
	err := fs.Parse(orderFlags(fs, args))
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

// orderFlags moves every flag ahead of the arguments. The standard flag package
// stops parsing at the first non-flag argument, so without this a flag written
// after the arguments is dropped in silence: `bot pair dev-bot --force` would
// pair as if --force were absent. Every widely used Go CLI, the go command
// included, accepts a flag in either position. A "--" ends the flags, so
// everything after it stays an argument.
func orderFlags(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	arguments := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			arguments = append(arguments, args[i:]...)
			return append(flags, arguments...)
		case !isFlag(arg):
			arguments = append(arguments, arg)
		default:
			flags = append(flags, arg)
			if takesValue(fs, arg) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	return append(flags, arguments...)
}

// isFlag reports whether an argument is a flag rather than a value. A lone "-"
// is an argument, by the same convention the flag package follows.
func isFlag(arg string) bool {
	return len(arg) > 1 && arg[0] == '-'
}

// takesValue reports whether a flag consumes the argument that follows it. A
// flag written as --name=value carries its own, and a boolean flag never takes
// one: IsBoolFlag is the same distinction the flag package makes.
func takesValue(fs *flag.FlagSet, arg string) bool {
	name, _, carriesValue := strings.Cut(arg, "=")
	if carriesValue {
		return false
	}
	defined := fs.Lookup(strings.TrimLeft(name, "-"))
	if defined == nil {
		return false
	}
	boolean, ok := defined.Value.(interface{ IsBoolFlag() bool })
	return !ok || !boolean.IsBoolFlag()
}
