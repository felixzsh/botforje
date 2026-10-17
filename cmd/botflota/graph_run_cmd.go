package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// graphRunListCmd is an API client: graph runs are stored executions the
// running instance reads, never local files (ADR 0025).
type graphRunListCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
	chatID *int64
}

func newGraphRunListCmd() *graphRunListCmd {
	fs := flag.NewFlagSet("graph-run list", flag.ContinueOnError)
	fs.Usage = func() {}
	return &graphRunListCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		chatID: fs.Int64("chat-id", 0, "`ID` chat whose graph run to read"),
	}
}

func runGraphRunList(args []string) int {
	cmd := newGraphRunListCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() != 1 || *cmd.chatID <= 0 {
		printHelp(os.Stderr, "graph-run", "list")
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
	query := graphRunQuery{scope: scopeName, bot: botName, chatID: *cmd.chatID}
	if err := printGraphRun(context.Background(), os.Stdout, client, query); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type graphRunQuery struct {
	scope  string
	bot    string
	chatID int64
}

func (q graphRunQuery) path() string {
	values := url.Values{"chat_id": {strconv.FormatInt(q.chatID, 10)}}
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/bots/" +
		url.PathEscape(q.bot) + "/graph-runs?" + values.Encode()
}

func printGraphRun(
	ctx context.Context, w io.Writer, client *apiClient, q graphRunQuery,
) error {
	var body struct {
		GraphRun struct {
			ID           int64     `json:"id"`
			ChatID       int64     `json:"chat_id"`
			Node         string    `json:"node"`
			VisitedNodes []string  `json:"visited_nodes"`
			CreatedAt    time.Time `json:"created_at"`
			UpdatedAt    time.Time `json:"updated_at"`
			ExpiresAt    time.Time `json:"expires_at"`
		} `json:"graph_run"`
	}
	if err := client.get(ctx, q.path(), &body); err != nil {
		return err
	}
	run := body.GraphRun
	fmt.Fprintf(w, "%-12s %-8s %-10s %-24s %-19s %-19s %s\n",
		"GRAPH_RUN_ID", "CHAT_ID", "NODE", "VISITED_NODES",
		"CREATED", "UPDATED", "EXPIRES")
	fmt.Fprintf(w, "%-12d %-8d %-10s %-24s %-19s %-19s %s\n",
		run.ID, run.ChatID, dash(run.Node),
		dash(strings.Join(run.VisitedNodes, ",")),
		run.CreatedAt.Local().Format("2006-01-02 15:04:05"),
		run.UpdatedAt.Local().Format("2006-01-02 15:04:05"),
		run.ExpiresAt.Local().Format("2006-01-02 15:04:05"))
	return nil
}
