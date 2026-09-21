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

// chatSyncCmd is an API client: the running fleet owns the live connection
// (ADR 0025).
type chatSyncCmd struct {
	fs      *flag.FlagSet
	config  *string
	scope   *string
	count   *int
	timeout *int
	all     *bool
}

func newChatSyncCmd() *chatSyncCmd {
	fs := flag.NewFlagSet("chat sync", flag.ContinueOnError)
	fs.Usage = func() {}
	return &chatSyncCmd{
		fs:      fs,
		config:  configFlag(fs),
		scope:   fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		count:   fs.Int("count", 0, "`N` older messages to import per chat (0 = all accessible)"),
		timeout: fs.Int("timeout", 90, "`SECONDS` to wait for the primary device, per chat"),
		all:     fs.Bool("all", false, "sync every chat of the bot, one at a time"),
	}
}

func runChatSync(args []string) int {
	cmd := newChatSyncCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 1 || (!*cmd.all && cmd.fs.NArg() < 2) {
		printHelp(os.Stderr, "chat", "sync")
		return 2
	}
	botName := cmd.fs.Arg(0)
	phone := ""
	if !*cmd.all {
		phone = cmd.fs.Arg(1)
	}
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *cmd.all {
		return syncEveryChat(ctx, os.Stdout, client, scopeName, botName,
			*cmd.count, *cmd.timeout)
	}
	return syncOneChat(ctx, os.Stdout, client, scopeName, botName, phone,
		*cmd.count, *cmd.timeout)
}

type chatSyncResult struct {
	Received  int  `json:"received"`
	Rounds    int  `json:"rounds"`
	Imported  int  `json:"imported"`
	Duplicate int  `json:"duplicate"`
	Skipped   int  `json:"skipped"`
	Stable    bool `json:"stable"`
	Exhausted bool `json:"exhausted"`
}

func syncOneChat(
	ctx context.Context, w io.Writer, client *apiClient, scope, bot, phone string,
	count, timeoutSeconds int,
) int {
	if count > 0 {
		fmt.Fprintf(w, "requesting up to %d older messages for %s through the fleet...\n",
			count, phone)
	} else {
		fmt.Fprintf(w, "requesting all accessible older messages for %s through the fleet...\n",
			phone)
	}
	result, err := requestChatSync(ctx, client, scope, bot, phone, count, timeoutSeconds)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	line := fmt.Sprintf("received %d in %d batch(es), imported %d (duplicate %d, skipped %d)",
		result.Received, result.Rounds, result.Imported, result.Duplicate, result.Skipped)
	switch {
	case result.Exhausted:
		line += "; stopped: the phone reports no more access"
	case result.Stable:
		line += "; stopped: nothing older to page"
	}
	fmt.Fprintln(w, line)
	return 0
}

// syncEveryChat goes one chat at a time (the fleet keeps one sync running per
// bot) and keeps walking when one fails (ADR 0035).
func syncEveryChat(
	ctx context.Context, w io.Writer, client *apiClient, scope, bot string,
	count, timeoutSeconds int,
) int {
	phones, err := listChatPhones(ctx, client, scope, bot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(phones) == 0 {
		fmt.Fprintln(w, "no chats to sync")
		return 0
	}
	fmt.Fprintf(w, "syncing %d chat(s) through the fleet...\n", len(phones))
	total := chatSyncResult{}
	failed := 0
	for _, phone := range phones {
		if err := ctx.Err(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		result, err := requestChatSync(ctx, client, scope, bot, phone, count, timeoutSeconds)
		if err != nil {
			failed++
			fmt.Fprintf(w, "%-15s failed: %v\n", phone, err)
			continue
		}
		total.Imported += result.Imported
		total.Duplicate += result.Duplicate
		total.Skipped += result.Skipped
		fmt.Fprintf(w, "%-15s imported %d (duplicate %d, skipped %d)%s\n",
			phone, result.Imported, result.Duplicate, result.Skipped, stopReason(result))
	}
	fmt.Fprintf(w, "synced %d chat(s), imported %d (duplicate %d, skipped %d), %d failed\n",
		len(phones)-failed, total.Imported, total.Duplicate, total.Skipped, failed)
	return 0
}

func requestChatSync(
	ctx context.Context, client *apiClient, scope, bot, phone string,
	count, timeoutSeconds int,
) (chatSyncResult, error) {
	values := url.Values{
		"bot":     {bot},
		"sender":  {phone},
		"count":   {strconv.Itoa(count)},
		"timeout": {strconv.Itoa(timeoutSeconds)},
	}
	path := "/api/v1/scopes/" + url.PathEscape(scope) + "/chats/sync?" + values.Encode()
	var result chatSyncResult
	timeout := time.Duration(timeoutSeconds) * time.Second
	if err := client.post(ctx, path, &result, syncHTTPTimeout(count, timeout)); err != nil {
		return result, err
	}
	return result, nil
}

func listChatPhones(
	ctx context.Context, client *apiClient, scope, bot string,
) ([]string, error) {
	var body struct {
		Chats []struct {
			Sender struct {
				Phone string `json:"phone"`
			} `json:"sender"`
		} `json:"chats"`
	}
	query := chatsQuery{scope: scope, bot: bot, limit: 0}
	if err := client.get(ctx, query.path(), &body); err != nil {
		return nil, err
	}
	phones := make([]string, 0, len(body.Chats))
	for _, chat := range body.Chats {
		if chat.Sender.Phone != "" {
			phones = append(phones, chat.Sender.Phone)
		}
	}
	return phones, nil
}

func stopReason(result chatSyncResult) string {
	switch {
	case result.Exhausted:
		return " [no more access]"
	case result.Stable:
		return " [nothing older]"
	default:
		return ""
	}
}

// syncHTTPTimeout prices one batch per fifty requested entries plus headroom;
// the phone decides how long a backfill takes, so an unbounded request gets a
// generous ceiling.
func syncHTTPTimeout(count int, perBatch time.Duration) time.Duration {
	if count <= 0 {
		return 30 * time.Minute
	}
	batches := (count + 49) / 50
	return time.Duration(batches)*perBatch + 30*time.Second
}

type chatsQuery struct {
	scope string
	bot   string
	limit int
}

func (q chatsQuery) path() string {
	values := url.Values{
		"bot":   {q.bot},
		"limit": {strconv.Itoa(q.limit)},
	}
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/chats?" + values.Encode()
}

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
