package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// chatCmd lists one bot's stored chats through the API: each chat is a sender
// the bot has a thread with, keyed by (scope, bot, sender) (ADR 0026).
type chatCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
	limit  *int
}

func newChatListCmd() *chatCmd {
	fs := flag.NewFlagSet("chat list", flag.ContinueOnError)
	fs.Usage = func() {}
	return &chatCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		limit:  fs.Int("limit", 50, "`N` chats to fetch"),
	}
}

func runChatList(args []string) int {
	cmd := newChatListCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 1 {
		printHelp(os.Stderr, "chat", "list")
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
	query := chatsQuery{scope: scopeName, bot: botName, limit: *cmd.limit}
	if err := printChats(context.Background(), os.Stdout, client, query); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// chatSyncCmd asks the primary device for older messages of one chat. It is an
// API client: the running fleet owns the live connection, so the same endpoint
// serves a dashboard later (ADR 0025, ADR 0035).
type chatSyncCmd struct {
	fs      *flag.FlagSet
	config  *string
	scope   *string
	count   *int
	timeout *int
}

func newChatSyncCmd() *chatSyncCmd {
	fs := flag.NewFlagSet("chat sync", flag.ContinueOnError)
	fs.Usage = func() {}
	return &chatSyncCmd{
		fs:      fs,
		config:  configFlag(fs),
		scope:   fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		count:   fs.Int("count", 0, "`N` older messages to import (0 = all accessible)"),
		timeout: fs.Int("timeout", 90, "`SECONDS` to wait for the primary device to answer"),
	}
}

func runChatSync(args []string) int {
	cmd := newChatSyncCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 2 {
		printHelp(os.Stderr, "chat", "sync")
		return 2
	}
	botName, phone := cmd.fs.Arg(0), cmd.fs.Arg(1)
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
	if *cmd.count > 0 {
		fmt.Printf("requesting up to %d older messages for %s through the fleet...\n",
			*cmd.count, phone)
	} else {
		fmt.Printf("requesting all accessible older messages for %s through the fleet...\n",
			phone)
	}
	values := url.Values{
		"bot":     {botName},
		"sender":  {phone},
		"count":   {strconv.Itoa(*cmd.count)},
		"timeout": {strconv.Itoa(*cmd.timeout)},
	}
	path := "/api/v1/scopes/" + url.PathEscape(scopeName) + "/chats/sync?" + values.Encode()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var result struct {
		Received  int  `json:"received"`
		Rounds    int  `json:"rounds"`
		Imported  int  `json:"imported"`
		Duplicate int  `json:"duplicate"`
		Skipped   int  `json:"skipped"`
		Stable    bool `json:"stable"`
		Exhausted bool `json:"exhausted"`
	}
	timeout := time.Duration(*cmd.timeout) * time.Second
	if err := client.post(ctx, path, &result, syncHTTPTimeout(*cmd.count, timeout)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("received %d in %d batch(es), imported %d (duplicate %d, skipped %d)",
		result.Received, result.Rounds, result.Imported, result.Duplicate, result.Skipped)
	switch {
	case result.Exhausted:
		fmt.Print("; stopped: the phone reports no more access")
	case result.Stable:
		fmt.Print("; stopped: nothing older to page")
	}
	fmt.Println()
	return 0
}

// syncHTTPTimeout bounds the whole backfill call: one batch per fifty requested
// entries plus headroom. An unbounded request gets a generous ceiling, since
// the phone decides how much history exists.
func syncHTTPTimeout(count int, perBatch time.Duration) time.Duration {
	if count <= 0 {
		return 30 * time.Minute
	}
	batches := (count + 49) / 50
	return time.Duration(batches)*perBatch + 30*time.Second
}

// chatsQuery addresses one bot's chats in the API.
type chatsQuery struct {
	scope string
	bot   string
	limit int
}

// path builds the chats route with its filters.
func (q chatsQuery) path() string {
	values := url.Values{
		"bot":   {q.bot},
		"limit": {strconv.Itoa(q.limit)},
	}
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/chats?" + values.Encode()
}

// printChats writes one row per chat, newest activity first, as the API
// returns them: the sender's phone and name, the stored message total split by
// ingest kind, and the first and last activity.
func printChats(ctx context.Context, w io.Writer, client *apiClient, q chatsQuery) error {
	var body struct {
		Chats []struct {
			Sender struct {
				Phone string `json:"phone"`
				Name  string `json:"name"`
			} `json:"sender"`
			Messages  int64     `json:"messages"`
			Sync      int64     `json:"sync"`
			Live      int64     `json:"live"`
			StartedAt time.Time `json:"started_at"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"chats"`
	}
	if err := client.get(ctx, q.path(), &body); err != nil {
		return err
	}
	fmt.Fprintf(w, "%-15s %-20s %8s %6s %6s  %-19s  %s\n",
		"PHONE", "NAME", "MESSAGES", "SYNC", "LIVE", "STARTED", "UPDATED")
	for _, chat := range body.Chats {
		fmt.Fprintf(w, "%-15s %-20s %8d %6d %6d  %-19s  %s\n",
			chat.Sender.Phone, dash(chat.Sender.Name), chat.Messages,
			chat.Sync, chat.Live,
			chat.StartedAt.Local().Format("2006-01-02 15:04:05"),
			chat.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return nil
}
