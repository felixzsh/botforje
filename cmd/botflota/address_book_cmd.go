package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"time"
)

type addressBookCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
	limit  *int
}

func newAddressBookListCmd() *addressBookCmd {
	fs := flag.NewFlagSet("address-book list", flag.ContinueOnError)
	fs.Usage = func() {}
	return &addressBookCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		limit:  fs.Int("limit", 200, "`N` entries to fetch"),
	}
}

func runAddressBookList(args []string) int {
	cmd := newAddressBookListCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 1 {
		printHelp(os.Stderr, "address-book", "list")
		return 2
	}
	botName := cmd.fs.Arg(0)
	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	scopeName, _, err := findBot(cfg, botName, *cmd.scope)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	client, err := newAPIClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	query := addressBookQuery{scope: scopeName, bot: botName, limit: *cmd.limit}
	if err := printAddressBook(context.Background(), os.Stdout, client, query); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type addressBookQuery struct {
	scope string
	bot   string
	limit int
}

func (q addressBookQuery) path() string {
	values := url.Values{"limit": {strconv.Itoa(q.limit)}}
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/bots/" +
		url.PathEscape(q.bot) + "/contacts?" + values.Encode()
}

func printAddressBook(
	ctx context.Context, w io.Writer, client *apiClient, q addressBookQuery,
) error {
	var body struct {
		Entries []struct {
			Provider    string    `json:"provider"`
			Subject     string    `json:"subject"`
			Phone       string    `json:"phone"`
			DisplayName string    `json:"display_name"`
			UpdatedAt   time.Time `json:"updated_at"`
		} `json:"contacts"`
	}
	if err := client.get(ctx, q.path(), &body); err != nil {
		return err
	}
	fmt.Fprintf(w, "%-12s %-18s %-24s %s\n",
		"PROVIDER", "SUBJECT", "NAME", "UPDATED")
	for _, entry := range body.Entries {
		fmt.Fprintf(w, "%-12s %-18s %-24s %s\n",
			dash(entry.Provider), entry.Subject, dash(entry.DisplayName),
			entry.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return nil
}
