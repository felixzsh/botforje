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

// contactCmd lists the address book one bot imported through the API. It is a
// separate resource from a chat: a contact is an identity the linked account
// knows, whether or not it ever wrote (ADR 0037).
type contactCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
	limit  *int
}

func newContactListCmd() *contactCmd {
	fs := flag.NewFlagSet("contact list", flag.ContinueOnError)
	fs.Usage = func() {}
	return &contactCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		limit:  fs.Int("limit", 200, "`N` contacts to fetch"),
	}
}

func runContactList(args []string) int {
	cmd := newContactListCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 1 {
		printHelp(os.Stderr, "contact", "list")
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
	query := contactQuery{scope: scopeName, bot: botName, limit: *cmd.limit}
	if err := printContacts(context.Background(), os.Stdout, client, query); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// contactQuery addresses one bot's contacts in the API.
type contactQuery struct {
	scope string
	bot   string
	limit int
}

// path builds the contacts route with its filters.
func (q contactQuery) path() string {
	values := url.Values{
		"bot":   {q.bot},
		"limit": {strconv.Itoa(q.limit)},
	}
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/contacts?" + values.Encode()
}

// printContacts writes one row per contact, most recently updated first, with
// the name a reader shows and the names behind it.
func printContacts(
	ctx context.Context, w io.Writer, client *apiClient, q contactQuery,
) error {
	var body struct {
		Contacts []struct {
			Phone        string    `json:"phone"`
			DisplayName  string    `json:"display_name"`
			PushName     string    `json:"push_name"`
			BusinessName string    `json:"business_name"`
			UpdatedAt    time.Time `json:"updated_at"`
		} `json:"contacts"`
	}
	if err := client.get(ctx, q.path(), &body); err != nil {
		return err
	}
	fmt.Fprintf(w, "%-15s %-24s %-20s %-20s %s\n",
		"PHONE", "NAME", "PUSH NAME", "BUSINESS NAME", "UPDATED")
	for _, contact := range body.Contacts {
		fmt.Fprintf(w, "%-15s %-24s %-20s %-20s %s\n",
			contact.Phone, dash(contact.DisplayName), dash(contact.PushName),
			dash(contact.BusinessName),
			contact.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return nil
}
