package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/config"
)

type pairCmd struct {
	fs            *flag.FlagSet
	config        *string
	scope         *string
	force         *bool
	importHistory *bool
}

func newPairCmd() *pairCmd {
	fs := flag.NewFlagSet("bot pair", flag.ContinueOnError)
	fs.Usage = func() {}
	return &pairCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		force:  fs.Bool("force", false, "replace an existing pairing"),
		importHistory: fs.Bool("import-history", false,
			"ask the phone for the full history window and wait for it"),
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

// botListEntry is one row of bot list, in table and JSON form.
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

	logger := botflota.NewLogger(cfg.LogLevel, os.Stderr)
	jid, err := whatsapp.Pair(context.Background(), whatsapp.PairOptions{
		Scope:       scopeName,
		Bot:         name,
		Devices:     devices,
		Accounts:    db.LinkedAccounts(),
		Pairings:    db.Pairings(),
		Senders:     db.Senders(),
		Chats:       db.Chats(),
		Messages:    db.Messages(),
		History:     historyEnabled(botSpec.Settings),
		FullHistory: *cmd.importHistory,
		Log:         logger,
		Force:       *cmd.force,
		DeviceName:  cfg.DeviceName,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("bot %q (scope %q) is paired as %s\n", name, scopeName, whatsapp.Number(jid))
	return 0
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

func runBotList(args []string) int {
	cmd := newBotListCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	db, err := openDatabase(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()

	ctx := context.Background()
	if err := whatsapp.ValidateAccountOwners(ctx, db.LinkedAccounts()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	devices, err := openDevices(ctx, db, nil, cfg.LogLevelDeps)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	entries := make([]botListEntry, 0)
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
		for _, botName := range config.SortedKeys(cfg.Scopes[scopeName].Bots) {
			state := botflota.PairingNotPaired
			phone := ""
			account, err := db.LinkedAccounts().Get(ctx, scopeName, botName)
			switch {
			case errors.Is(err, botflota.ErrNotFound):
			case err != nil:
				fmt.Fprintln(os.Stderr, err)
				return 1
			default:
				ready, err := whatsapp.HasLocalDevice(ctx, devices, account)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				if !ready {
					state = botflota.PairingUnpaired
				} else {
					state = botflota.PairingPaired
					phone = whatsapp.Number(account.JID)
				}
			}
			pairingStatus, err := db.Pairings().Get(ctx, scopeName, botName)
			if err != nil && !errors.Is(err, botflota.ErrNotFound) {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			entry := botListEntry{
				Bot: botName, Scope: scopeName, Phone: phone, Status: string(state),
			}
			if err == nil && pairingStatus.State == state {
				lastChange := pairingStatus.LastChange
				entry.LastChange = &lastChange
			}
			entries = append(entries, entry)
		}
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
