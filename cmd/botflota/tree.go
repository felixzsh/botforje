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
							"pairing state, and last change. The running instance answers\n" +
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
						name:    "pair",
						summary: "pair a bot by scanning a QR code",
						long: "Link a bot to WhatsApp by scanning a QR code. The command\n" +
							"rejects a phone number already owned by another bot. While the\n" +
							"instance runs it pairs through the API of fleet run, so the bot\n" +
							"joins the fleet as soon as the scan is accepted; with the fleet\n" +
							"stopped it pairs locally under the instance lock. An already\n" +
							"paired bot is reported and left alone; replacing a pairing is a\n" +
							"local operation with --force, which registers again and is also\n" +
							"the only way to change what the registration carries.\n" +
							"Whatever WhatsApp delivers while the pairing connection is alive\n" +
							"is stored; --import-history also asks for the full history window\n" +
							"and waits for it, which is what brings the older chats. Without\n" +
							"it the default recent window arrives and `chat sync` pages\n" +
							"older messages per chat.\n" +
							"The command answers with what it stored: the chats and messages\n" +
							"the history sync brought, and the contacts the address book\n" +
							"added. --verbose prints the structured log of a local pairing;\n" +
							"a pairing through the instance logs to fleet run.",
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
						summary: "ask the phone for older messages of one chat or all of them",
						long: "Ask the primary device for older messages and import batches\n" +
							"until the cursor stops moving. --count limits the total per chat;\n" +
							"zero means all accessible history. The running fleet owns the live\n" +
							"connection, so this is an API client and fleet run must be up.\n" +
							"--all walks every chat of the bot, newest activity first, one at a\n" +
							"time, printing a line per chat and a final total; the command takes\n" +
							"only the bot, and a chat that fails is reported and skipped.",
						usage: "botflota chat sync [options] <bot> [sender]",
						examples: []string{
							"botflota chat sync support-bot 5215500000000",
							"botflota chat sync --count 75 support-bot 5215500000000",
							"botflota chat sync --all support-bot",
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
							"The running instance answers through the REST API, so fleet run\n" +
							"must be up. The bot is named as in bot list; add --scope when\n" +
							"the name exists in more than one scope.",
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

func childNamed(node *commandHelp, name string) *commandHelp {
	for i := range node.sub {
		if node.sub[i].name == name {
			return &node.sub[i]
		}
	}
	return nil
}
