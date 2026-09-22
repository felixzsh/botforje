package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

type pairCmd struct {
	fs            *flag.FlagSet
	config        *string
	scope         *string
	force         *bool
	importHistory *bool
	verbose       *bool
}

func newPairCmd() *pairCmd {
	fs := flag.NewFlagSet("bot pair", flag.ContinueOnError)
	fs.Usage = func() {}
	return &pairCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		force:  fs.Bool("force", false, "discard the local pairing and register again"),
		importHistory: fs.Bool("import-history", false,
			"ask the phone for the full history window and wait for it"),
		verbose: fs.Bool("verbose", false,
			"print the structured log at the configured log_level"),
	}
}

type unpairCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
}

func newUnpairCmd() *unpairCmd {
	fs := flag.NewFlagSet("bot unpair", flag.ContinueOnError)
	fs.Usage = func() {}
	return &unpairCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
	}
}

type botListCmd struct {
	fs     *flag.FlagSet
	config *string
	json   *bool
}

func newBotListCmd() *botListCmd {
	fs := flag.NewFlagSet("bot list", flag.ContinueOnError)
	fs.Usage = func() {}
	return &botListCmd{
		fs:     fs,
		config: configFlag(fs),
		json:   fs.Bool("json", false, "machine-readable output"),
	}
}

type botListEntry struct {
	Bot        string     `json:"bot"`
	Scope      string     `json:"scope"`
	Phone      string     `json:"phone,omitempty"`
	Status     string     `json:"status"`
	LastChange *time.Time `json:"last_change,omitempty"`
}

func runPair(args []string) int {
	cmd := newPairCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 1 {
		printHelp(os.Stderr, "bot", "pair")
		return 2
	}
	name := cmd.fs.Arg(0)

	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	scopeName, botSpec, err := findBot(cfg, name, *cmd.scope)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		printHelp(os.Stderr, "bot", "pair")
		return 1
	}
	lock, err := lockInstance(cfg)
	if err != nil {
		reportOwned(err, "pairing (one device cannot be connected twice)")
		return 1
	}
	defer releaseInstance(lock, func(err error) { fmt.Fprintln(os.Stderr, err) })
	db, err := openDatabase(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	ctx := context.Background()
	// Pairing is idempotent: an existing link is reported and left alone.
	if account, err := db.LinkedAccounts().Get(ctx, scopeName, name); err == nil && !*cmd.force {
		fmt.Printf("bot %q (scope %q) is already paired as %s; "+
			"use --force to replace the pairing\n",
			name, scopeName, whatsapp.Number(account.JID))
		return 0
	}
	logger := pairLogger(cfg, *cmd.verbose)
	var deps *slog.Logger
	if *cmd.verbose {
		deps = logger
	}
	devices, err := openDevices(ctx, db, deps, cfg.LogLevelDeps)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	result, err := whatsapp.Pair(ctx, whatsapp.PairOptions{
		Scope:          scopeName,
		Bot:            name,
		Devices:        devices,
		Accounts:       db.LinkedAccounts(),
		Pairings:       db.Pairings(),
		Senders:        db.Senders(),
		Chats:          db.Chats(),
		Messages:       db.Messages(),
		History:        historyEnabled(botSpec.Settings),
		FullHistory:    *cmd.importHistory,
		Contacts:       db.Contacts(),
		ImportContacts: contactsEnabled(botSpec.Settings),
		Log:            logger,
		DepsLogLevel:   botflota.ParseLogLevel(cfg.LogLevelDeps),
		Force:          *cmd.force,
		DeviceName:     cfg.DeviceName,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("bot %q (scope %q) is paired as %s\n", name, scopeName,
		whatsapp.Number(result.JID))
	printPairImport(os.Stdout, botSpec.Settings, result)
	return 0
}

// pairLogger is the logger of a one-shot command: without --verbose only
// warnings and errors surface (ADR 0038).
func pairLogger(cfg *config.Config, verbose bool) *slog.Logger {
	if !verbose {
		return botflota.NewLogger("warn", os.Stderr)
	}
	return botflota.NewLogger(cfg.LogLevel, os.Stderr)
}

// printPairImport is the command's result, not a log line, so it never
// depends on the log level.
func printPairImport(w io.Writer, settings *spec.BotSettings, result whatsapp.PairResult) {
	if historyEnabled(settings) {
		if result.History.Chunks == 0 {
			fmt.Fprintln(w, "history: nothing arrived")
		} else {
			fmt.Fprintf(w, "history: %d chats, %d messages (%d duplicate, %d skipped) in %s\n",
				result.History.Conversations, result.History.Imported,
				result.History.Duplicate, result.History.Skipped,
				result.Elapsed.Round(time.Second))
		}
	}
	if contactsEnabled(settings) {
		fmt.Fprintf(w, "contacts: %d imported\n", result.Contacts)
	}
}

func runUnpair(args []string) int {
	cmd := newUnpairCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 1 {
		printHelp(os.Stderr, "bot", "unpair")
		return 2
	}
	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	name := cmd.fs.Arg(0)
	scopeName, _, err := findBot(cfg, name, *cmd.scope)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		printHelp(os.Stderr, "bot", "unpair")
		return 1
	}
	lock, err := lockInstance(cfg)
	if err != nil {
		reportOwned(err, "unpairing")
		return 1
	}
	defer releaseInstance(lock, func(err error) { fmt.Fprintln(os.Stderr, err) })
	db, err := openDatabase(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	devices, err := openDevices(context.Background(), db, nil, cfg.LogLevelDeps)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := whatsapp.Unpair(context.Background(), whatsapp.UnpairOptions{
		Scope: scopeName, Bot: name, Devices: devices,
		Accounts: db.LinkedAccounts(), Pairings: db.Pairings(),
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("bot %q (scope %q) local pairing removed\n", name, scopeName)
	return 0
}

// listBots asks the running instance for its bots; `bot list` is an instance
// query and never opens the database (ADR 0039).
func listBots(ctx context.Context, client *apiClient) ([]botListEntry, error) {
	var body struct {
		Bots []struct {
			Scope      string     `json:"scope"`
			Bot        string     `json:"bot"`
			Phone      string     `json:"phone"`
			Pairing    string     `json:"pairing"`
			LastChange *time.Time `json:"last_change"`
		} `json:"bots"`
	}
	if err := client.get(ctx, "/api/v1/bots", &body); err != nil {
		return nil, err
	}
	entries := make([]botListEntry, 0, len(body.Bots))
	for _, bot := range body.Bots {
		status := bot.Pairing
		if status == "" {
			status = string(botflota.PairingNotPaired)
		}
		entries = append(entries, botListEntry{
			Bot: bot.Bot, Scope: bot.Scope, Phone: bot.Phone,
			Status: status, LastChange: bot.LastChange,
		})
	}
	return entries, nil
}

func runBotList(args []string) int {
	cmd := newBotListCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	client, err := newAPIClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	entries, err := listBots(context.Background(), client)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *cmd.json {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(entries); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	fmt.Printf("%-24s %-12s %-16s %-12s %s\n",
		"BOT", "SCOPE", "PHONE", "STATUS", "LAST CHANGE")
	for _, entry := range entries {
		printStatusRow(entry)
	}
	return 0
}

func printStatusRow(entry botListEntry) {
	phone := entry.Phone
	if phone == "" {
		phone = "-"
	}
	lastChange := time.Time{}
	if entry.LastChange != nil {
		lastChange = *entry.LastChange
	}
	fmt.Printf("%-24s %-12s %-16s %-12s %s\n",
		entry.Bot, entry.Scope, phone, entry.Status, formatLastChange(lastChange))
}

func formatLastChange(lastChange time.Time) string {
	if lastChange.IsZero() {
		return "-"
	}
	return lastChange.Local().Format("2006-01-02 15:04:05")
}
