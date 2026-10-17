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

	"github.com/felixzsh/botflota/config"
)

// findScope resolves a scope-only command's scope: an explicit flag wins,
// and a config with a single scope supplies it so the flag stays optional.
// A config with several scopes and no flag is ambiguous, like a repeated
// bot name without --scope (ADR 0054).
func findScope(cfg *config.Config, scope string) (string, error) {
	if scope != "" {
		if _, ok := cfg.Scopes[scope]; !ok {
			return "", fmt.Errorf("unknown scope %q", scope)
		}
		return scope, nil
	}
	if len(cfg.Scopes) == 1 {
		for name := range cfg.Scopes {
			return name, nil
		}
	}
	return "", fmt.Errorf(
		"--scope is required when the config holds several scopes")
}

type peerListCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
	limit  *int
}

func newPeerListCmd() *peerListCmd {
	fs := flag.NewFlagSet("peer list", flag.ContinueOnError)
	fs.Usage = func() {}
	return &peerListCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope to list (needed if the config holds several)"),
		limit:  fs.Int("limit", 50, "`N` peers to fetch"),
	}
}

func runPeerList(args []string) int {
	cmd := newPeerListCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() > 0 {
		printHelp(os.Stderr, "peer", "list")
		return 2
	}
	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	scopeName, err := findScope(cfg, *cmd.scope)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	client, err := newAPIClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	query := peersQuery{scope: scopeName, limit: *cmd.limit}
	if err := printPeers(context.Background(), os.Stdout, client, query); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type peerShowCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
	peerID *int64
}

func newPeerShowCmd() *peerShowCmd {
	fs := flag.NewFlagSet("peer show", flag.ContinueOnError)
	fs.Usage = func() {}
	return &peerShowCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the peer"),
		peerID: fs.Int64("peer-id", 0, "`ID` scope-owned peer to read"),
	}
}

func runPeerShow(args []string) int {
	cmd := newPeerShowCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	// The peer id is scope-owned, so it is always required: a subject
	// alone is never a cross-provider lookup key (ADR 0044).
	if cmd.fs.NArg() > 0 || *cmd.peerID <= 0 {
		printHelp(os.Stderr, "peer", "show")
		return 2
	}
	cfg, code := loadConfig(*cmd.config)
	if code != 0 {
		return code
	}
	scopeName, err := findScope(cfg, *cmd.scope)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	client, err := newAPIClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	query := peerQuery{scope: scopeName, id: *cmd.peerID}
	if err := printPeer(context.Background(), os.Stdout, client, query); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type peerRecord struct {
	ID                int64     `json:"id"`
	Provider          string    `json:"provider"`
	Subject           string    `json:"subject"`
	DisplayName       string    `json:"display_name"`
	FirstSeen         time.Time `json:"first_seen"`
	LastSeen          time.Time `json:"last_seen"`
	DirectoryImported bool      `json:"directory_imported"`
	HasChat           bool      `json:"has_chat"`
	HasHistory        bool      `json:"has_history"`
}

type peersQuery struct {
	scope string
	limit int
}

func (q peersQuery) path() string {
	values := url.Values{"limit": {strconv.Itoa(q.limit)}}
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/peers?" + values.Encode()
}

type peerQuery struct {
	scope string
	id    int64
}

func (q peerQuery) path() string {
	return "/api/v1/scopes/" + url.PathEscape(q.scope) + "/peers/" +
		strconv.FormatInt(q.id, 10)
}

func printPeers(
	ctx context.Context, w io.Writer, client *apiClient, q peersQuery,
) error {
	var body struct {
		Peers []peerRecord `json:"peers"`
	}
	if err := client.get(ctx, q.path(), &body); err != nil {
		return err
	}
	fmt.Fprintf(w, "%-8s %-12s %-18s %-20s %-19s %-19s %-5s %-5s %-5s\n",
		"ID", "PROVIDER", "SUBJECT", "NAME", "FIRST_SEEN", "LAST_SEEN",
		"DIR", "CHAT", "HIST")
	for _, peer := range body.Peers {
		fmt.Fprintf(w, "%-8d %-12s %-18s %-20s %-19s %-19s %-5v %-5v %-5v\n",
			peer.ID, dash(peer.Provider), dash(peer.Subject),
			dash(peer.DisplayName), peerTime(peer.FirstSeen),
			peerTime(peer.LastSeen), peer.DirectoryImported,
			peer.HasChat, peer.HasHistory)
	}
	return nil
}

func printPeer(
	ctx context.Context, w io.Writer, client *apiClient, q peerQuery,
) error {
	var peer peerRecord
	if err := client.get(ctx, q.path(), &peer); err != nil {
		return err
	}
	fmt.Fprintf(w, "%-8s %-12s %-18s %-20s %-19s %-19s %-5s %-5s %-5s\n",
		"ID", "PROVIDER", "SUBJECT", "NAME", "FIRST_SEEN", "LAST_SEEN",
		"DIR", "CHAT", "HIST")
	fmt.Fprintf(w, "%-8d %-12s %-18s %-20s %-19s %-19s %-5v %-5v %-5v\n",
		peer.ID, dash(peer.Provider), dash(peer.Subject),
		dash(peer.DisplayName), peerTime(peer.FirstSeen),
		peerTime(peer.LastSeen), peer.DirectoryImported,
		peer.HasChat, peer.HasHistory)
	return nil
}

// peerTime renders a zero first/last seen as absent: the identity was known
// through an import or an API send, never a live inbound (ADR 0029).
func peerTime(at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	return at.Local().Format("2006-01-02 15:04:05")
}
