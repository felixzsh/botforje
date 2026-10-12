package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/felixzsh/botflota"
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
	Bot        string     `json:"bot"`
	Scope      string     `json:"scope"`
	Phone      string     `json:"phone,omitempty"`
	Status     string     `json:"status"`
	LastChange *time.Time `json:"last_change,omitempty"`
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
