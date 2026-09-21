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
	"strings"
	"syscall"
	"time"
)

// messageCmd's tail --follow polls with a cursor instead of streaming.
type messageCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
	limit  *int
	follow *bool
}

func newMessageCmd(name string, follow bool) *messageCmd {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {}
	cmd := &messageCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		limit:  fs.Int("limit", 50, "`N` messages to fetch"),
	}
	if follow {
		cmd.follow = fs.Bool("follow", false, "keep polling for new messages")
	}
	return cmd
}

func runMessageList(args []string) int {
	return runMessage(newMessageCmd("message list", false), args)
}

func runMessageTail(args []string) int {
	return runMessage(newMessageCmd("message tail", true), args)
}

func runMessage(cmd *messageCmd, args []string) int {
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	path := strings.Fields(cmd.fs.Name())
	if cmd.fs.NArg() < 2 {
		printHelp(os.Stderr, path...)
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
	query := messageQuery{
		scope: scopeName, bot: botName, phone: phone, limit: *cmd.limit,
	}
	ctx := context.Background()
	lastID, err := printMessages(ctx, os.Stdout, client, query, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if cmd.follow == nil || !*cmd.follow {
		return 0
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := followMessages(ctx, os.Stdout, client, query, lastID, followInterval); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

const followInterval = 2 * time.Second

type messageQuery struct {
	scope string
	bot   string
	phone string
	limit int
}

func (q messageQuery) path(afterID int64) string {
	values := url.Values{
		"bot":    {q.bot},
		"sender": {q.phone},
		"limit":  {strconv.Itoa(q.limit)},
	}
	if afterID > 0 {
		values.Set("after_id", strconv.FormatInt(afterID, 10))
	}
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/messages?" + values.Encode()
}

// printMessages returns the last id seen: the cursor of the next poll.
func printMessages(
	ctx context.Context, w io.Writer, client *apiClient, q messageQuery, afterID int64,
) (int64, error) {
	var body struct {
		Messages []struct {
			ID         int64     `json:"id"`
			Direction  string    `json:"direction"`
			Kind       string    `json:"kind"`
			IngestKind string    `json:"ingest_kind"`
			Origin     string    `json:"origin"`
			Routing    string    `json:"routing"`
			Body       string    `json:"body"`
			At         time.Time `json:"at"`
		} `json:"messages"`
	}
	if err := client.get(ctx, q.path(afterID), &body); err != nil {
		return afterID, err
	}
	last := afterID
	for _, message := range body.Messages {
		fmt.Fprintf(w, "%-19s  %-3s  %-10s  %-7s  %-8s  %-16s  %s\n",
			message.At.Local().Format("2006-01-02 15:04:05"),
			message.Direction, dash(message.Kind), dash(message.IngestKind),
			dash(message.Origin), dash(message.Routing), dash(message.Body))
		if message.ID > last {
			last = message.ID
		}
	}
	return last, nil
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func followMessages(
	ctx context.Context, w io.Writer, client *apiClient, q messageQuery,
	lastID int64, every time.Duration,
) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			next, err := printMessages(ctx, w, client, q, lastID)
			if err != nil {
				if ctx.Err() != nil {
					return nil // interrupted mid-poll
				}
				return err
			}
			lastID = next
		}
	}
}
