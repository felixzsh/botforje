package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

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
	Bot     string `json:"bot"`
	Scope   string `json:"scope"`
	Binding string `json:"binding,omitempty"`
	State   string `json:"state,omitempty"`
}

// listBots asks the running instance for its bots; `bot list` is an instance
// query and never opens the database (ADR 0039).
func listBots(ctx context.Context, client *apiClient) ([]botListEntry, error) {
	var body struct {
		Bots []struct {
			Scope   string `json:"scope"`
			Bot     string `json:"bot"`
			State   string `json:"state"`
			Binding *struct {
				Provider string `json:"provider"`
				Identity struct {
					Subject string `json:"subject"`
				} `json:"identity"`
			} `json:"binding"`
		} `json:"bots"`
	}
	if err := client.get(ctx, "/api/v1/bots", &body); err != nil {
		return nil, err
	}
	entries := make([]botListEntry, 0, len(body.Bots))
	for _, bot := range body.Bots {
		binding := "-"
		if bot.Binding != nil {
			binding = bot.Binding.Provider + "/" + dash(bot.Binding.Identity.Subject)
		}
		state := bot.State
		if state == "" {
			state = "-"
		}
		entries = append(entries, botListEntry{
			Bot: bot.Bot, Scope: bot.Scope, Binding: binding, State: state,
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
	fmt.Printf("%-24s %-12s %-32s %s\n",
		"BOT", "SCOPE", "BINDING", "STATE")
	for _, entry := range entries {
		printStatusRow(entry)
	}
	return 0
}

func printStatusRow(entry botListEntry) {
	fmt.Printf("%-24s %-12s %-32s %s\n",
		entry.Bot, entry.Scope, entry.Binding, entry.State)
}
