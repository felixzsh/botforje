package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/felixzsh/botflota/internal/config"
	"github.com/felixzsh/botflota/internal/domain"
	"github.com/felixzsh/botflota/internal/whatsapp"
)

type pairCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
	force  *bool
}

func newPairCmd() *pairCmd {
	fs := flag.NewFlagSet("bot pair", flag.ContinueOnError)
	fs.Usage = func() {}
	return &pairCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		force:  fs.Bool("force", false, "replace an existing pairing"),
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
}

func newBotListCmd() *botListCmd {
	fs := flag.NewFlagSet("bot list", flag.ContinueOnError)
	fs.Usage = func() {}
	return &botListCmd{fs: fs, config: configFlag(fs)}
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
	scopeName, _, err := findBot(cfg, name, *cmd.scope)
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

	jid, err := whatsapp.Pair(context.Background(), whatsapp.PairOptions{
		Scope:    scopeName,
		Bot:      name,
		DB:       db.DB,
		Accounts: db.Accounts(),
		Force:    *cmd.force,
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
	if err := whatsapp.Unpair(context.Background(), whatsapp.UnpairOptions{
		Scope: scopeName, Bot: name, DB: db.DB, Accounts: db.Accounts(),
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
	if err := whatsapp.ValidateAccountOwners(ctx, db.Accounts()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%-24s %-12s %-16s %-12s %s\n",
		"BOT", "SCOPE", "PHONE", "STATUS", "LAST CHANGE")
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
		for _, botName := range config.SortedKeys(cfg.Scopes[scopeName].Bots) {
			state := domain.PairingNotPaired
			phone := "-"
			account, err := db.Accounts().Get(ctx, scopeName, botName)
			switch {
			case errors.Is(err, domain.ErrNotFound):
			case err != nil:
				fmt.Fprintln(os.Stderr, err)
				return 1
			default:
				ready, err := whatsapp.HasLocalDevice(ctx, db.DB, account)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				if !ready {
					state = domain.PairingUnpaired
				} else {
					state = domain.PairingPaired
					phone = whatsapp.Number(account.JID)
				}
			}
			pairingStatus, err := db.Accounts().GetPairingStatus(ctx, scopeName, botName)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			lastChange := time.Time{}
			if err == nil && pairingStatus.State == state {
				lastChange = pairingStatus.LastChange
			}
			printStatusRow(botName, scopeName, phone, string(state), lastChange)
		}
	}
	return 0
}

func printStatusRow(bot, scope, phone, status string, lastChange time.Time) {
	fmt.Printf("%-24s %-12s %-16s %-12s %s\n",
		bot, scope, phone, status, formatLastChange(lastChange))
}

func formatLastChange(lastChange time.Time) string {
	if lastChange.IsZero() {
		return "-"
	}
	return lastChange.Local().Format("2006-01-02 15:04:05")
}
