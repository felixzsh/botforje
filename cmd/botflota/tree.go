package main

import "flag"

// commandHelp is one node of the CLI tree. Routing and help both read it, so
// a command cannot exist in one and not the other.
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

func cliTree() *commandHelp {
	return &commandHelp{
		name:    "botflota",
		summary: "Declarative messaging bot fleet runner.",
		usage:   "botflota <command> [options]",
		examples: []string{
			"botflota config validate",
			"botflota bot list",
			"botflota bot bind support-bot",
			"botflota fleet run",
			"botflota fleet status",
			"botflota chat list support-bot",
			"botflota directory list support-bot",
			"botflota peer list",
			"botflota graph-run list support-bot --chat-id 3",
			"botflota message tail support-bot --chat-id 3 --follow",
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
				long:    "List binding state, bind a bot to its channel account, and remove a local binding.",
				usage:   "botflota bot <command> [options]",
				examples: []string{
					"botflota bot list",
					"botflota bot bind support-bot",
					"botflota bot unbind support-bot",
				},
				moreHelp: "for more help: botflota bot <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "list configured bots and their binding",
						long: "Print a table of configured bots with their binding\n" +
							"and connection state. The running instance answers\n" +
							"through the REST API, so fleet run must be up; it does not\n" +
							"change stored state.",
						usage: "botflota bot list [options]",
						examples: []string{
							"botflota bot list",
							"botflota bot list --config etc/config.yml",
						},
						flags: func() *flag.FlagSet { return newBotListCmd().fs },
						run:   runBotList,
					},
					{
						name:    "bind",
						summary: "bind a bot to its channel account",
						long: "Drive the bot's neutral account setup: discover the method,\n" +
							"begin a session, and answer its steps. Display steps render\n" +
							"here (a display challenge prints to the terminal); input steps prompt\n" +
							"for their typed values, which travel only toward the\n" +
							"instance and are never printed back. While the instance\n" +
							"runs it binds through the API of fleet run, so the bot\n" +
							"joins the fleet as soon as setup commits; with the fleet\n" +
							"stopped it binds locally under the instance lock. An\n" +
							"already bound bot is reported and left alone; replacing a\n" +
							"binding is --force, which detaches first and binds again.\n" +
							"--param passes method parameters as name=value pairs.",
						usage: "botflota bot bind [options] <bot>",
						examples: []string{
							"botflota bot bind support-bot",
							"botflota bot bind --method qr support-bot",
							"botflota bot bind --scope dev support-bot",
							"botflota bot bind --force support-bot",
							"botflota bot bind --param full_history=true support-bot",
						},
						flags: func() *flag.FlagSet { return newBindCmd().fs },
						run:   runBind,
					},
					{
						name:    "unbind",
						summary: "remove a bot's local binding",
						long: "Detach the committed binding: the running bot stops first,\n" +
							"then the local binding is forgotten. Chats, directory, and\n" +
							"history stay; the provider account itself is never\n" +
							"destroyed.",
						usage: "botflota bot unbind [options] <bot>",
						examples: []string{
							"botflota bot unbind support-bot",
							"botflota bot unbind --scope dev support-bot",
						},
						flags: func() *flag.FlagSet { return newUnbindCmd().fs },
						run:   runUnbind,
					},
				},
			},
			{
				name:    "fleet",
				summary: "run and manage the fleet",
				long:    "Run the fleet of bound bots and inspect the running one.",
				usage:   "botflota fleet <command> [options]",
				examples: []string{
					"botflota fleet run",
					"botflota fleet status",
				},
				moreHelp: "for more help: botflota fleet <command> --help",
				sub: []commandHelp{
					{
						name:    "run",
						summary: "run every bound bot until interrupted",
						long: "Start every bound bot, answer inbound messages with the\n" +
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
				long:    "List the stored chats of one bot and ask the account for older history.",
				usage:   "botflota chat <command> [options]",
				examples: []string{
					"botflota chat list support-bot",
					"botflota chat sync support-bot --chat-id 3",
				},
				moreHelp: "for more help: botflota chat <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "list one bot's chats and their peers",
						long: "Print one row per chat with its peer reference and name, the\n" +
							"stored message total split into sync and live, and the first\n" +
							"and last activity, newest activity first. The running instance\n" +
							"answers through the REST API, so fleet run must be up. The bot\n" +
							"is named as in bot list; add --scope when the name exists in\n" +
							"more than one scope.",
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
						summary: "ask the account for older messages of one chat or all of them",
						long: "Ask the linked account for older messages and import batches\n" +
							"until the cursor stops moving. --count limits the total per chat;\n" +
							"zero means all accessible history. The running fleet owns the live\n" +
							"connection, so this is an API client and fleet run must be up.\n" +
							"--all walks every chat of the bot, newest activity first, one at a\n" +
							"time, printing a line per chat and a final total; the command takes\n" +
							"only the bot, and a chat that fails is reported and skipped.",
						usage: "botflota chat sync [options] <bot>",
						examples: []string{
							"botflota chat sync support-bot --chat-id 3",
							"botflota chat sync --count 75 support-bot --chat-id 3",
							"botflota chat sync --all support-bot",
						},
						flags: func() *flag.FlagSet { return newChatSyncCmd().fs },
						run:   runChatSync,
					},
				},
			},
			{
				name:    "directory",
				summary: "list the directory a bot imported",
				long: "List the entries Botflota copied from one bot's linked\n" +
					"account. An entry is an identity the account knows, whether or\n" +
					"not it ever wrote; it is not a chat. Only a bot that imports a\n" +
					"directory has any.",
				usage: "botflota directory <command> [options]",
				examples: []string{
					"botflota directory list support-bot",
				},
				moreHelp: "for more help: botflota directory <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "print one bot's imported directory",
						long: "Print one row per entry, most recently updated first, with\n" +
							"the peer subject and the name. The running instance answers\n" +
							"through the REST API, so fleet run must be up. The bot is\n" +
							"named as in bot list; add --scope when the name exists in more\n" +
							"than one scope.",
						usage: "botflota directory list [options] <bot>",
						examples: []string{
							"botflota directory list support-bot",
							"botflota directory list --limit 500 support-bot",
						},
						flags: func() *flag.FlagSet { return newDirectoryListCmd().fs },
						run:   runDirectoryList,
					},
				},
			},
			{
				name:    "peer",
				summary: "read a scope's peer identities",
				long: "List the messaging identities a scope knows and read one\n" +
					"by its scope-owned id. A peer is an identity the scope\n" +
					"recorded, whether or not it ever wrote; it is not a chat.\n" +
					"First and last seen stay empty without a live inbound.",
				usage: "botflota peer <command> [options]",
				examples: []string{
					"botflota peer list",
					"botflota peer show --peer-id 7",
				},
				moreHelp: "for more help: botflota peer <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "print a scope's peer identities",
						long: "Print one row per peer, most recently active first, with\n" +
							"the neutral reference and name, first and last live\n" +
							"inbound, and whether a directory import, chat, or history\n" +
							"recorded it. The running instance answers through the\n" +
							"REST API, so fleet run must be up. Add --scope when the\n" +
							"config holds more than one scope.",
						usage: "botflota peer list [options]",
						examples: []string{
							"botflota peer list",
							"botflota peer list --scope dev --limit 100",
						},
						flags: func() *flag.FlagSet { return newPeerListCmd().fs },
						run:   runPeerList,
					},
					{
						name:    "show",
						summary: "read one peer by its scope-owned id",
						long: "Print the peer the scope-owned id names: its neutral\n" +
							"reference and name, first and last live inbound, and\n" +
							"whether a directory import, chat, or history recorded\n" +
							"it. The running instance answers through the REST API,\n" +
							"so fleet run must be up. --peer-id is always required: a\n" +
							"subject alone never names a peer across providers.",
						usage: "botflota peer show [options]",
						examples: []string{
							"botflota peer show --peer-id 7",
							"botflota peer show --scope dev --peer-id 7",
						},
						flags: func() *flag.FlagSet { return newPeerShowCmd().fs },
						run:   runPeerShow,
					},
				},
			},
			{
				name:    "graph-run",
				summary: "read a chat's graph run",
				long: "Read the stored graph execution of one chat through the\n" +
					"REST API. A graph run is one bounded execution of the bot's\n" +
					"graph for the chat, never a setup session.",
				usage: "botflota graph-run <command> [options]",
				examples: []string{
					"botflota graph-run list support-bot --chat-id 3",
				},
				moreHelp: "for more help: botflota graph-run <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "print one chat's graph run",
						long: "Print the graph run of one chat with its current node,\n" +
							"visited nodes, and times. The bot is named as in bot\n" +
							"list; add --scope when the name exists in more than one\n" +
							"scope.",
						usage: "botflota graph-run list [options] <bot>",
						examples: []string{
							"botflota graph-run list support-bot --chat-id 3",
						},
						flags: func() *flag.FlagSet { return newGraphRunListCmd().fs },
						run:   runGraphRunList,
					},
				},
			},
			{
				name:    "message",
				summary: "read a chat's message history",
				long:    "Read the stored messages of one chat through the REST API.",
				usage:   "botflota message <command> [options]",
				examples: []string{
					"botflota message list support-bot --chat-id 3",
					"botflota message tail support-bot --chat-id 3 --follow",
				},
				moreHelp: "for more help: botflota message <command> --help",
				sub: []commandHelp{
					{
						name:    "list",
						summary: "print the newest messages of one chat",
						long: "Print the last messages of one chat, oldest first. The bot\n" +
							"is named as in bot list; add --scope when the name exists in\n" +
							"more than one scope.",
						usage: "botflota message list [options] <bot>",
						examples: []string{
							"botflota message list support-bot --chat-id 3",
							"botflota message list --limit 100 support-bot --chat-id 3",
						},
						flags: func() *flag.FlagSet { return newMessageCmd("message list", false).fs },
						run:   runMessageList,
					},
					{
						name:    "tail",
						summary: "follow one chat's messages",
						long: "Print the newest messages and, with --follow, keep polling\n" +
							"for new ones until interrupted.",
						usage: "botflota message tail [options] <bot>",
						examples: []string{
							"botflota message tail support-bot --chat-id 3",
							"botflota message tail --follow support-bot --chat-id 3",
						},
						flags: func() *flag.FlagSet { return newMessageCmd("message tail", true).fs },
						run:   runMessageTail,
					},
				},
			},
		},
	}
}

func childNamed(node *commandHelp, name string) *commandHelp {
	for i := range node.sub {
		if node.sub[i].name == name {
			return &node.sub[i]
		}
	}
	return nil
}
