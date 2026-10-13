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
	chatID  *int64
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
		chatID:  fs.Int64("chat-id", 0, "`ID` chat to sync (required unless --all)"),
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
	if cmd.fs.NArg() < 1 {
		printHelp(os.Stderr, "chat", "sync")
		return 2
	}
	if !*cmd.all && *cmd.chatID <= 0 {
		fmt.Fprintln(os.Stderr, "chat sync needs --chat-id, or --all for every chat")
		printHelp(os.Stderr, "chat", "sync")
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *cmd.all {
		return syncEveryChat(ctx, os.Stdout, client, scopeName, botName,
			*cmd.count, *cmd.timeout)
	}
	return syncOneChat(ctx, os.Stdout, client, scopeName, botName, *cmd.chatID,
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
	ctx context.Context, w io.Writer, client *apiClient, scope, bot string,
	chatID int64, count, timeoutSeconds int,
) int {
	label := "chat " + strconv.FormatInt(chatID, 10)
	if count > 0 {
		fmt.Fprintf(w, "requesting up to %d older messages for %s through the fleet...\n",
			count, label)
	} else {
		fmt.Fprintf(w, "requesting all accessible older messages for %s through the fleet...\n",
			label)
	}
	result, err := requestChatSync(ctx, client, scope, bot, chatID, count, timeoutSeconds)
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
	ids, err := listChatIDs(ctx, client, scope, bot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(ids) == 0 {
		fmt.Fprintln(w, "no chats to sync")
		return 0
	}
	fmt.Fprintf(w, "syncing %d chat(s) through the fleet...\n", len(ids))
	total := chatSyncResult{}
	failed := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		result, err := requestChatSync(ctx, client, scope, bot, id, count, timeoutSeconds)
		if err != nil {
			failed++
			fmt.Fprintf(w, "%-8d failed: %v\n", id, err)
			continue
		}
		total.Imported += result.Imported
		total.Duplicate += result.Duplicate
		total.Skipped += result.Skipped
		fmt.Fprintf(w, "%-8d imported %d (duplicate %d, skipped %d)%s\n",
			id, result.Imported, result.Duplicate, result.Skipped, stopReason(result))
	}
	fmt.Fprintf(w, "synced %d chat(s), imported %d (duplicate %d, skipped %d), %d failed\n",
		len(ids)-failed, total.Imported, total.Duplicate, total.Skipped, failed)
	return 0
}

func requestChatSync(
	ctx context.Context, client *apiClient, scope, bot string, chatID int64,
	count, timeoutSeconds int,
) (chatSyncResult, error) {
	values := url.Values{
		"chat_id": {strconv.FormatInt(chatID, 10)},
		"count":   {strconv.Itoa(count)},
		"timeout": {strconv.Itoa(timeoutSeconds)},
	}
	path := "/api/v1/scopes/" + url.PathEscape(scope) + "/bots/" +
		url.PathEscape(bot) + "/chats/sync?" + values.Encode()
	var result chatSyncResult
	timeout := time.Duration(timeoutSeconds) * time.Second
	if err := client.post(ctx, path, &result, syncHTTPTimeout(count, timeout)); err != nil {
		return result, err
	}
	return result, nil
}

func listChatIDs(
	ctx context.Context, client *apiClient, scope, bot string,
) ([]int64, error) {
	var body struct {
		Chats []struct {
			ID int64 `json:"id"`
		} `json:"chats"`
	}
	query := chatsQuery{scope: scope, bot: bot, limit: 0}
	if err := client.get(ctx, query.path(), &body); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(body.Chats))
	for _, chat := range body.Chats {
		if chat.ID != 0 {
			ids = append(ids, chat.ID)
		}
	}
	return ids, nil
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
	values := url.Values{"limit": {strconv.Itoa(q.limit)}}
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/bots/" +
		url.PathEscape(q.bot) + "/chats?" + values.Encode()
}

func printChats(ctx context.Context, w io.Writer, client *apiClient, q chatsQuery) error {
	var body struct {
		Chats []struct {
			ID   int64 `json:"id"`
			Peer struct {
				Provider string `json:"provider"`
				Subject  string `json:"subject"`
				Name     string `json:"name"`
			} `json:"peer"`
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
	fmt.Fprintf(w, "%-8s %-12s %-18s %-20s %8s %6s %6s  %-19s  %s\n",
		"ID", "PROVIDER", "SUBJECT", "NAME", "MESSAGES", "SYNC", "LIVE",
		"STARTED", "UPDATED")
	for _, chat := range body.Chats {
		fmt.Fprintf(w, "%-8d %-12s %-18s %-20s %8d %6d %6d  %-19s  %s\n",
			chat.ID, dash(chat.Peer.Provider), dash(chat.Peer.Subject),
			dash(chat.Peer.Name), chat.Messages, chat.Sync, chat.Live,
			chat.StartedAt.Local().Format("2006-01-02 15:04:05"),
			chat.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return nil
}
