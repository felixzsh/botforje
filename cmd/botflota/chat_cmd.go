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

	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/config"
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

// chatSyncCmd asks the primary device for older messages of one chat. It is a
// bounded experiment (ADR 0035) and opens the device directly, so the fleet
// must be stopped.
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
		count:   fs.Int("count", 50, "`N` older messages to request (WhatsApp recommends <= 50)"),
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
	if fleetLooksRunning(cfg) {
		fmt.Fprintln(os.Stderr,
			"the fleet looks like it is running; stop it before chat sync "+
				"(one device cannot be connected twice)")
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
	timeout := time.Duration(*cmd.timeout) * time.Second
	fmt.Printf("requesting up to %d older messages for %s; waiting up to %s...\n",
		*cmd.count, phone, timeout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := whatsapp.SyncHistory(ctx, whatsapp.SyncOptions{
		Scope:    scopeName,
		Bot:      botName,
		Devices:  devices,
		Accounts: db.LinkedAccounts(),
		Senders:  db.Senders(),
		Chats:    db.Chats(),
		Messages: db.Messages(),
		Phone:    phone,
		Count:    *cmd.count,
		Timeout:  timeout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("imported %d (duplicate %d, skipped %d)\n",
		result.Imported, result.Duplicate, result.Skipped)
	return 0
}

// fleetLooksRunning reports whether the API answers, so a command that opens
// the device can refuse instead of disrupting the connected fleet. Any error
// means "not running" and lets the command continue.
func fleetLooksRunning(cfg *config.Config) bool {
	if cfg.Port == 0 {
		return false
	}
	client, err := newAPIClient(cfg)
	if err != nil {
		return false
	}
	_, err = client.getRaw(context.Background(), "/api/v1/status")
	return err == nil
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
