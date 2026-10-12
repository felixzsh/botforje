package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal"
	"golang.org/x/term"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/sqlite"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/adapters/whatsapp/channel"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/instance"
	"github.com/felixzsh/botflota/spec"
)

// bindCmd binds a bot to its channel account through the neutral setup
// lifecycle (ADR 0053, ADR 0054): discover the method, begin a session, and
// drive its steps — display, input, approval, profile — without a
// provider-specific form. --param carries method parameters as name=value
// pairs (for example full_history=true); an adapter with existing step
// kinds needs no CLI handler.
type bindCmd struct {
	fs         *flag.FlagSet
	config     *string
	scope      *string
	method     *string
	requestKey *string
	params     paramList
	force      *bool
	verbose    *bool
}

type paramList []string

func (p *paramList) String() string     { return strings.Join(*p, ",") }
func (p *paramList) Set(v string) error { *p = append(*p, v); return nil }

func newBindCmd() *bindCmd {
	fs := flag.NewFlagSet("bot bind", flag.ContinueOnError)
	fs.Usage = func() {}
	cmd := &bindCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
		method: fs.String("method", "",
			"`NAME` setup method; empty picks the only advertised method"),
		requestKey: fs.String("request-key", "",
			"`KEY` idempotency key for the setup start; empty mints one"),
		force: fs.Bool("force", false, "detach the committed binding and bind again"),
		verbose: fs.Bool("verbose", false,
			"print the structured log at the configured log_level"),
	}
	fs.Var(&cmd.params, "param",
		"`NAME=VALUE` setup parameter, repeatable (for example full_history=true)")
	return cmd
}

type unbindCmd struct {
	fs     *flag.FlagSet
	config *string
	scope  *string
}

func newUnbindCmd() *unbindCmd {
	fs := flag.NewFlagSet("bot unbind", flag.ContinueOnError)
	fs.Usage = func() {}
	return &unbindCmd{
		fs:     fs,
		config: configFlag(fs),
		scope:  fs.String("scope", "", "`NAME` scope that owns the bot (needed if the name repeats)"),
	}
}

// setupSession is the CLI's view of a neutral setup session; it mirrors the
// API's sanitized session, which never carries submitted secret values.
type setupSession struct {
	ID              string     `json:"id"`
	Method          string     `json:"method"`
	State           string     `json:"state"`
	Revision        int64      `json:"revision"`
	ErrorCode       string     `json:"error_code"`
	ActivationError string     `json:"activation_error"`
	Step            *setupStep `json:"step"`
}

type setupStep struct {
	Kind    string `json:"kind"`
	Display *struct {
		Format  string `json:"format"`
		Payload string `json:"payload"`
		Hint    string `json:"hint"`
	} `json:"display"`
	Fields []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Label string `json:"label"`
	} `json:"fields"`
	Approval *struct {
		Destination string `json:"destination"`
		Instruction string `json:"instruction"`
	} `json:"approval"`
	Profile *struct {
		Methods     []string `json:"methods"`
		Options     []string `json:"options"`
		AllowCreate bool     `json:"allow_create"`
		Params      []struct {
			Name  string `json:"name"`
			Kind  string `json:"kind"`
			Label string `json:"label"`
		} `json:"params"`
	} `json:"profile"`
}

type bindingStatus struct {
	Binding *struct {
		Provider   string `json:"provider"`
		Mode       string `json:"mode"`
		Generation int64  `json:"generation"`
		Identity   struct {
			Provider string `json:"provider"`
			Subject  string `json:"subject"`
		} `json:"identity"`
	} `json:"binding"`
	Condition string `json:"condition"`
	Method    string `json:"method"`
}

// bindEndpoints is the neutral lifecycle over either transport: the running
// instance's API or the locked offline stores. Both speak the same session
// contract, so the driver never branches on provider.
type bindEndpoints interface {
	inspect(ctx context.Context, scope, bot string) (bindingStatus, error)
	detach(ctx context.Context, scope, bot string) error
	methods(ctx context.Context, scope, bot string) ([]string, error)
	begin(ctx context.Context, scope, bot, method, key string,
		params map[string]string) (*setupSession, error)
	read(ctx context.Context, scope, bot, id string) (*setupSession, error)
	input(ctx context.Context, scope, bot, id string, revision int64,
		key string, values map[string]string) (*setupSession, error)
}

type bindOptions struct {
	method     string
	requestKey string
	params     map[string]string
	force      bool
}

// bindPollInterval is how often the CLI re-reads a session it is driving;
// display payloads rotate on their own, so a missed one is never final.
const bindPollInterval = 500 * time.Millisecond

func driveBind(
	ctx context.Context, ep bindEndpoints, scope, bot string, opts bindOptions,
	stdin io.Reader, stdout io.Writer,
) int {
	status, err := ep.inspect(ctx, scope, bot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// The method resolves before any detach: an argument error must never
	// alter durable state (ADR 0053).
	if status.Binding != nil && !opts.force &&
		status.Condition != string(botflota.ReadinessActionRequired) {
		fmt.Fprintf(stdout, "bot %q (scope %q) is already bound as %s/%s; "+
			"use --force to replace the binding\n",
			bot, scope, status.Binding.Identity.Provider,
			dash(status.Binding.Identity.Subject))
		return 0
	}
	method, ok := resolveSetupMethod(ctx, ep, scope, bot, opts.method, status.Method)
	if !ok {
		return 1
	}
	if status.Binding != nil && opts.force {
		if err := ep.detach(ctx, scope, bot); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	if status.Binding != nil && !opts.force {
		// Reauthorization keeps the committed association: a binding that
		// needs its owner enters a new setup session instead of a
		// replacement (ADR 0053).
		fmt.Fprintf(stdout, "bot %q (scope %q) requires action; "+
			"reauthorizing without detaching\n", bot, scope)
	}
	key := opts.requestKey
	if key == "" {
		key = "bind-" + randomHex(8)
	}
	session, err := ep.begin(ctx, scope, bot, method, key, opts.params)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	reader := bufio.NewReader(stdin)
	lastDisplay := ""
	for {
		switch session.State {
		case "succeeded":
			return reportBound(ctx, ep, scope, bot, session, stdout)
		case "failed":
			fmt.Fprintln(os.Stderr, "setup failed: "+dash(session.ErrorCode))
			return 1
		case "cancelled":
			fmt.Fprintln(os.Stderr, "the setup session was cancelled")
			return 1
		case "expired":
			fmt.Fprintln(os.Stderr, "the setup session expired; run bot bind again")
			return 1
		}
		if session.Step == nil {
			time.Sleep(bindPollInterval)
		} else {
			switch session.Step.Kind {
			case "display":
				if session.Step.Display != nil &&
					session.Step.Display.Payload != lastDisplay {
					renderSetupDisplay(stdout, session.Step.Display.Format,
						session.Step.Display.Payload, session.Step.Display.Hint)
					lastDisplay = session.Step.Display.Payload
				}
				time.Sleep(bindPollInterval)
			case "approval":
				if session.Step.Approval != nil {
					if session.Step.Approval.Destination != "" {
						fmt.Fprintln(stdout, "approve at "+
							session.Step.Approval.Destination)
					}
					if session.Step.Approval.Instruction != "" {
						fmt.Fprintln(stdout, session.Step.Approval.Instruction)
					}
				}
				time.Sleep(bindPollInterval)
			case "input", "profile":
				values, ok := answerSetupStep(session.Step, reader, stdin, stdout)
				if !ok {
					return 1
				}
				updated, err := ep.input(ctx, scope, bot, session.ID,
					session.Revision, "input-"+randomHex(8), values)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				session = updated
				continue
			default:
				fmt.Fprintf(os.Stderr, "unsupported setup step %q\n", session.Step.Kind)
				return 1
			}
		}
		updated, err := ep.read(ctx, scope, bot, session.ID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		session = updated
	}
}

// resolveSetupMethod picks the setup method without touching durable
// state: an explicit flag wins, then the only advertised method, then the
// readiness-appropriate method the inspection named (ADR 0053).
func resolveSetupMethod(
	ctx context.Context, ep bindEndpoints, scope, bot, flag, readiness string,
) (string, bool) {
	advertised, err := ep.methods(ctx, scope, bot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return "", false
	}
	if flag != "" {
		// An explicit method is checked against what the channel
		// offers before any detach: a rejected begin must never
		// follow a detach it did not need (ADR 0053).
		for _, method := range advertised {
			if method == flag {
				return flag, true
			}
		}
		fmt.Fprintf(os.Stderr,
			"unsupported setup method %q (offered: %s)\n",
			flag, strings.Join(advertised, ", "))
		return "", false
	}
	if len(advertised) == 1 {
		return advertised[0], true
	}
	for _, method := range advertised {
		if method == readiness && method != "" {
			return method, true
		}
	}
	if len(advertised) == 0 {
		fmt.Fprintln(os.Stderr, "that channel offers no account setup")
		return "", false
	}
	fmt.Fprintf(os.Stderr,
		"that channel offers several setup methods (%s); choose one with --method\n",
		strings.Join(advertised, ", "))
	return "", false
}

func reportBound(
	ctx context.Context, ep bindEndpoints, scope, bot string,
	session *setupSession, stdout io.Writer,
) int {
	if session.ActivationError != "" {
		fmt.Fprintf(stdout, "bot %q (scope %q) setup succeeded but did not activate: %s\n",
			bot, scope, session.ActivationError)
		return 0
	}
	status, err := ep.inspect(ctx, scope, bot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if status.Binding == nil {
		fmt.Fprintf(stdout, "bot %q (scope %q) setup succeeded\n", bot, scope)
		return 0
	}
	fmt.Fprintf(stdout, "bot %q (scope %q) is bound as %s/%s\n",
		bot, scope, status.Binding.Identity.Provider,
		dash(status.Binding.Identity.Subject))
	return 0
}

// secretFieldKind reports whether a setup input kind is write-only: its
// value is never echoed by the terminal it is typed on, matching the wire
// rule that submitted secrets never come back on a read (ADR 0053).
func secretFieldKind(kind string) bool {
	switch kind {
	case botflota.SetupFieldCredential, botflota.SetupFieldPassword,
		botflota.SetupFieldCode:
		return true
	}
	return false
}

// promptValue reads one typed value: secret kinds use no-echo terminal
// input when stdin is a terminal, and the plain reader otherwise, so pipes
// and tests keep working unchanged.
func promptValue(
	label, kind string, reader *bufio.Reader, stdin io.Reader, stdout io.Writer,
) (string, bool) {
	fmt.Fprintf(stdout, "%s: ", label)
	if file, ok := stdin.(*os.File); ok && secretFieldKind(kind) &&
		term.IsTerminal(int(file.Fd())) {
		raw, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return "", false
		}
		return string(raw), true
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		return "", false
	}
	return strings.TrimSpace(line), true
}

// answerSetupStep prompts for a step's typed values on stdout; secrets travel
// only toward the instance, never back into a response the CLI would print.
func answerSetupStep(
	step *setupStep, reader *bufio.Reader, stdin io.Reader, stdout io.Writer,
) (map[string]string, bool) {
	if step.Kind == "profile" && step.Profile != nil {
		return answerProfileStep(step, reader, stdin, stdout)
	}
	values := map[string]string{}
	for _, field := range step.Fields {
		label := field.Label
		if label == "" {
			label = field.Name
		}
		value, ok := promptValue(label, field.Kind, reader, stdin, stdout)
		if !ok {
			return nil, false
		}
		values[field.Name] = value
	}
	return values, true
}

func answerProfileStep(
	step *setupStep, reader *bufio.Reader, stdin io.Reader, stdout io.Writer,
) (map[string]string, bool) {
	profile := step.Profile
	for i, option := range profile.Options {
		fmt.Fprintf(stdout, "[%d] %s\n", i+1, option)
	}
	if profile.AllowCreate {
		fmt.Fprintln(stdout, "empty choice creates a new profile")
	}
	fmt.Fprint(stdout, "profile: ")
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		return nil, false
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		if !profile.AllowCreate {
			fmt.Fprintln(os.Stderr, "that setup step needs a profile choice")
			return nil, false
		}
		values := map[string]string{"profile": "new"}
		for _, param := range profile.Params {
			label := param.Label
			if label == "" {
				label = param.Name
			}
			value, ok := promptValue(label, param.Kind, reader, stdin, stdout)
			if !ok {
				return nil, false
			}
			values[param.Name] = value
		}
		return values, true
	}
	return map[string]string{"profile": choice}, true
}

func renderSetupDisplay(stdout io.Writer, format, payload, hint string) {
	switch format {
	case "qr":
		qrterminal.GenerateHalfBlock(payload, qrterminal.L, stdout)
	case "uri":
		fmt.Fprintln(stdout, "open "+payload)
	default:
		fmt.Fprintf(stdout, "setup challenge (%s)\n", dash(format))
	}
	if hint != "" {
		fmt.Fprintln(stdout, hint)
	}
}

func randomHex(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func runBind(args []string) int {
	cmd := newBindCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 1 {
		printHelp(os.Stderr, "bot", "bind")
		return 2
	}
	name := cmd.fs.Arg(0)

	// The instance is taken before the tree is read, so a local bind runs
	// cannot race the owner's (ADR 0039, ADR 0042).
	configFile, err := configPath(*cmd.config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	lock, err := ownInstance(configFile)
	owned := err == nil
	if err != nil && !errors.Is(err, instance.ErrOwned) {
		reportOwned(err, "binding (one device cannot be connected twice)")
		return 1
	}
	cfg, code := loadConfig(configFile)
	if code != 0 {
		return code
	}
	scopeName, botSpec, err := findBot(cfg, name, *cmd.scope)
	if err != nil {
		if owned {
			releaseInstance(lock, func(err error) { fmt.Fprintln(os.Stderr, err) })
		}
		fmt.Fprintln(os.Stderr, err)
		printHelp(os.Stderr, "bot", "bind")
		return 1
	}
	params := map[string]string{}
	for _, param := range cmd.params {
		name, value, ok := strings.Cut(param, "=")
		if !ok || name == "" {
			fmt.Fprintf(os.Stderr, "invalid --param %q, want name=value\n", param)
			return 2
		}
		params[name] = value
	}
	opts := bindOptions{
		method: *cmd.method, requestKey: *cmd.requestKey,
		params: params, force: *cmd.force,
	}
	if !owned {
		return bindThroughAPI(os.Stdout, os.Stdin, cfg, scopeName, name, opts)
	}
	defer releaseInstance(lock, func(err error) { fmt.Fprintln(os.Stderr, err) })
	return bindOffline(os.Stdout, os.Stdin, cfg, scopeName, name, botSpec, opts,
		*cmd.verbose, installedOfflineAssemblies())
}

func runUnbind(args []string) int {
	cmd := newUnbindCmd()
	if code := parseArgs(cmd.fs, args); code >= 0 {
		return code
	}
	if cmd.fs.NArg() < 1 {
		printHelp(os.Stderr, "bot", "unbind")
		return 2
	}
	// The instance is taken before the tree is read, so the recovery a local
	// unbind runs cannot race the owner's (ADR 0039, ADR 0042).
	configFile, err := configPath(*cmd.config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	lock, err := ownInstance(configFile)
	owned := err == nil
	if err != nil && !errors.Is(err, instance.ErrOwned) {
		reportOwned(err, "unbinding")
		return 1
	}
	cfg, code := loadConfig(configFile)
	if code != 0 {
		return code
	}
	name := cmd.fs.Arg(0)
	scopeName, botSpec, err := findBot(cfg, name, *cmd.scope)
	if err != nil {
		if owned {
			releaseInstance(lock, func(err error) { fmt.Fprintln(os.Stderr, err) })
		}
		fmt.Fprintln(os.Stderr, err)
		printHelp(os.Stderr, "bot", "unbind")
		return 1
	}
	if !owned {
		return unbindThroughAPI(os.Stdout, cfg, scopeName, name)
	}
	defer releaseInstance(lock, func(err error) { fmt.Fprintln(os.Stderr, err) })
	return unbindOffline(os.Stdout, cfg, scopeName, name, botSpec,
		installedOfflineAssemblies())
}

// unbindTimeout bounds the call: the instance stops the bot, draining its
// outbox, before it removes the binding.
const unbindTimeout = time.Minute

func bindingPath(scope, bot string) string {
	return "/api/v1/scopes/" + url.PathEscape(scope) + "/bots/" +
		url.PathEscape(bot) + "/binding"
}

func setupPath(scope, bot string) string { return bindingPath(scope, bot) + "/setups" }

// remoteBind drives the running instance's neutral lifecycle: the same
// operations a host backend uses, with challenges rendered here
// (ADR 0046, ADR 0053).
type remoteBind struct {
	client *apiClient
}

func (r remoteBind) inspect(
	ctx context.Context, scope, bot string,
) (bindingStatus, error) {
	var status bindingStatus
	err := r.client.get(ctx, bindingPath(scope, bot), &status)
	return status, err
}

func (r remoteBind) detach(ctx context.Context, scope, bot string) error {
	return r.client.delete(ctx, bindingPath(scope, bot), unbindTimeout)
}

func (r remoteBind) methods(ctx context.Context, scope, bot string) ([]string, error) {
	var body struct {
		Methods []string `json:"methods"`
	}
	err := r.client.get(ctx, bindingPath(scope, bot)+"/methods", &body)
	return body.Methods, err
}

func (r remoteBind) begin(
	ctx context.Context, scope, bot, method, key string, params map[string]string,
) (*setupSession, error) {
	var session setupSession
	err := r.client.postJSON(ctx, setupPath(scope, bot), map[string]any{
		"method": method, "request_key": key, "params": params,
	}, &session, apiTimeout)
	return &session, err
}

func (r remoteBind) read(
	ctx context.Context, scope, bot, id string,
) (*setupSession, error) {
	var session setupSession
	err := r.client.get(ctx, setupPath(scope, bot)+"/"+url.PathEscape(id), &session)
	return &session, err
}

func (r remoteBind) input(
	ctx context.Context, scope, bot, id string, revision int64,
	key string, values map[string]string,
) (*setupSession, error) {
	var session setupSession
	err := r.client.postJSON(ctx,
		setupPath(scope, bot)+"/"+url.PathEscape(id)+"/inputs", map[string]any{
			"revision": revision, "request_key": key, "values": values,
		}, &session, apiTimeout)
	return &session, err
}

func bindThroughAPI(
	stdout io.Writer, stdin io.Reader, cfg *config.Config, scopeName, botName string,
	opts bindOptions,
) int {
	client, err := newAPIClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stdin == nil {
		stdin = os.Stdin
	}
	return driveBind(context.Background(), remoteBind{client: client},
		scopeName, botName, opts, stdin, stdout)
}

// unbindThroughAPI drops the binding through the running instance — the
// same path a host backend uses: it stops the bot before the binding goes
// (ADR 0021, ADR 0053).
func unbindThroughAPI(
	w io.Writer, cfg *config.Config, scopeName, botName string,
) int {
	client, err := newAPIClient(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	err = client.delete(context.Background(), bindingPath(scopeName, botName),
		unbindTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(w, "bot %q (scope %q) unbound\n", botName, scopeName)
	return 0
}

// bindLogger is the logger of a one-shot command: without --verbose only
// warnings and errors surface (ADR 0038).
func bindLogger(cfg *config.Config, verbose bool) *slog.Logger {
	if !verbose {
		return botflota.NewLogger("warn", os.Stderr)
	}
	return botflota.NewLogger(cfg.LogLevel, os.Stderr)
}

// offlineAssembly opens one installed adapter's runtime dependencies and
// returns its catalog with setup wired. The offline flow selects by channel,
// so shipping another adapter registers one entry here instead of adding a
// branch to the bind/unbind flow (ADR 0052, ADR 0053).
type offlineAssembly struct {
	provider, mode string
	open           func(cfg *config.Config, db *sqlite.DB, bot spec.Bot,
		log *slog.Logger) (*channels.Catalog, func(), error)
}

// installedOfflineAssemblies lists the shipped adapters' offline assembly.
// Tests register fakes through the same list.
func installedOfflineAssemblies() []offlineAssembly {
	return []offlineAssembly{{
		provider: channel.Provider, mode: channel.Mode,
		open: openWhatsappOfflineCatalog,
	}}
}

func offlineChannelCatalog(
	cfg *config.Config, db *sqlite.DB, botSpec spec.Bot, logger *slog.Logger,
	assemblies []offlineAssembly,
) (*channels.Catalog, func(), error) {
	for _, asm := range assemblies {
		if botSpec.Channel.Provider == asm.provider &&
			botSpec.Channel.Mode == asm.mode {
			return asm.open(cfg, db, botSpec, logger)
		}
	}
	// Nothing installed serves this channel: resolve against the pure
	// catalog without opening any store, so the failure names the
	// missing installation instead of inventing state.
	catalog, err := pureCatalog()
	return catalog, func() {}, err
}

// openWhatsappOfflineCatalog opens the device store and wires the WhatsApp
// runtime catalog for one bot; no other channel touches these dependencies.
func openWhatsappOfflineCatalog(
	cfg *config.Config, db *sqlite.DB, _ spec.Bot, logger *slog.Logger,
) (*channels.Catalog, func(), error) {
	nop := func() {}
	ctx := context.Background()
	devices, err := openDevices(ctx, db, logger, cfg.LogLevelDeps)
	if err != nil {
		return nil, nop, err
	}
	manager := whatsapp.NewPairManager(whatsapp.PairManagerOptions{
		Build: offlinePairBuild(cfg, db, devices),
		OnPaired: func(context.Context, string, string) error {
			return errors.New("fleet is not running")
		},
		Log: logger,
	})
	catalog, err := runtimeCatalog(channel.Deps{
		Devices:      devices,
		Bindings:     db.Bindings(),
		Pairings:     db.Pairings(),
		Peers:        db.Peers(),
		Chats:        db.Chats(),
		Messages:     db.Messages(),
		Directory:    db.Directory(),
		Outbox:       db.Outbox(),
		Pairs:        manager,
		DepsLogLevel: botflota.ParseLogLevel(cfg.LogLevelDeps),
	})
	if err != nil {
		manager.Close()
		return nil, nop, err
	}
	return catalog, manager.Close, nil
}

// resolveOfflineSetup selects the bot's installed definition through the
// catalog and returns its neutral lifecycle and setup runner, decoding the
// bot's options without opening anything further (ADR 0052, ADR 0053).
func resolveOfflineSetup(
	catalog *channels.Catalog, scope, bot string, botSpec spec.Bot, log *slog.Logger,
) (botflota.AccountLifecycle, botflota.SetupRunner, error) {
	def, _, err := catalog.Resolve(botSpec.Channel)
	if err != nil {
		return nil, nil, err
	}
	if def.Setup == nil {
		return nil, nil, fmt.Errorf(
			"bot %q channel %s/%s offers no account setup: %w",
			bot, botSpec.Channel.Provider, botSpec.Channel.Mode, botflota.ErrNoSetup)
	}
	var options any
	if def.Decode != nil {
		var reasons []string
		options, reasons = def.Decode(botSpec)
		if len(reasons) > 0 {
			return nil, nil, fmt.Errorf("bot %q channel: %s",
				bot, strings.Join(reasons, "; "))
		}
	}
	return def.Setup(channels.Bind{
		Scope: scope, Bot: bot, Options: options, Log: log,
	})
}

// bindOffline binds under the instance lock through the bot's installed
// setup runner directly: the same neutral operations the API drives, with
// no provider-specific form (ADR 0053).
func bindOffline(
	stdout io.Writer, stdin io.Reader, cfg *config.Config,
	scopeName, botName string, botSpec spec.Bot, opts bindOptions, verbose bool,
	assemblies []offlineAssembly,
) int {
	ctx := context.Background()
	db, err := openDatabase(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	logger := bindLogger(cfg, verbose)
	catalog, cleanup, err := offlineChannelCatalog(
		cfg, db, botSpec, logger, assemblies)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer cleanup()
	lifecycle, runner, err := resolveOfflineSetup(
		catalog, scopeName, botName, botSpec, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return driveBind(ctx, directBind{lifecycle: lifecycle, runner: runner},
		scopeName, botName, opts, stdin, stdout)
}

func unbindOffline(
	w io.Writer, cfg *config.Config, scopeName, botName string, botSpec spec.Bot,
	assemblies []offlineAssembly,
) int {
	ctx := context.Background()
	db, err := openDatabase(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	logger := bindLogger(cfg, false)
	catalog, cleanup, err := offlineChannelCatalog(
		cfg, db, botSpec, logger, assemblies)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer cleanup()
	lifecycle, _, err := resolveOfflineSetup(
		catalog, scopeName, botName, botSpec, logger)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if lifecycle == nil {
		fmt.Fprintln(os.Stderr, "that channel offers no account lifecycle")
		return 1
	}
	status, err := lifecycle.InspectBinding(ctx, scopeName, botName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if status.Binding == nil {
		fmt.Fprintln(os.Stderr, "no committed binding for that bot")
		return 1
	}
	if err := lifecycle.DetachBinding(ctx, scopeName, botName); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(w, "bot %q (scope %q) unbound\n", botName, scopeName)
	return 0
}

// directBind drives the installed setup runner without leaving the process:
// the locked offline operator path speaks the same session contract as the
// API (ADR 0053).
type directBind struct {
	lifecycle botflota.AccountLifecycle
	runner    botflota.SetupRunner
}

func (d directBind) inspect(
	ctx context.Context, scope, bot string,
) (bindingStatus, error) {
	status, err := d.lifecycle.InspectBinding(ctx, scope, bot)
	if err != nil {
		return bindingStatus{}, err
	}
	return bindingStatusOf(status), nil
}

func (d directBind) detach(ctx context.Context, scope, bot string) error {
	return d.lifecycle.DetachBinding(ctx, scope, bot)
}

func (d directBind) methods(ctx context.Context, scope, bot string) ([]string, error) {
	return d.runner.SetupMethods(ctx, scope, bot)
}

func (d directBind) begin(
	ctx context.Context, scope, bot, method, key string, params map[string]string,
) (*setupSession, error) {
	session, err := d.runner.BeginSetup(ctx, scope, bot,
		botflota.SetupBegin{Method: method, RequestKey: key, Params: params})
	if err != nil {
		return nil, err
	}
	return setupSessionOf(session), nil
}

func (d directBind) read(
	ctx context.Context, scope, bot, id string,
) (*setupSession, error) {
	session, err := d.runner.ReadSetup(ctx, scope, bot, id)
	if err != nil {
		return nil, err
	}
	return setupSessionOf(session), nil
}

func (d directBind) input(
	ctx context.Context, scope, bot, id string, revision int64,
	key string, values map[string]string,
) (*setupSession, error) {
	session, err := d.runner.InputSetup(ctx, scope, bot, id, botflota.SetupInput{
		Revision: revision, RequestKey: key, Values: values,
	})
	if err != nil {
		return nil, err
	}
	return setupSessionOf(session), nil
}

func bindingStatusOf(status botflota.BindingStatus) bindingStatus {
	view := bindingStatus{Condition: string(status.Condition)}
	if status.Binding != nil {
		view.Binding = &struct {
			Provider   string `json:"provider"`
			Mode       string `json:"mode"`
			Generation int64  `json:"generation"`
			Identity   struct {
				Provider string `json:"provider"`
				Subject  string `json:"subject"`
			} `json:"identity"`
		}{
			Provider:   status.Binding.Provider,
			Mode:       status.Binding.Mode,
			Generation: status.Binding.Generation,
		}
		view.Binding.Identity.Provider = status.Binding.Identity.Provider
		view.Binding.Identity.Subject = status.Binding.Identity.Subject
	}
	return view
}

func setupSessionOf(session *botflota.SetupSession) *setupSession {
	view := &setupSession{
		ID: session.ID, Method: session.Method, State: string(session.State),
		Revision: session.Revision, ErrorCode: session.ErrorCode,
		ActivationError: session.ActivationError,
	}
	if session.Step != nil {
		step := &setupStep{Kind: session.Step.Kind}
		if session.Step.Display != nil {
			step.Display = &struct {
				Format  string `json:"format"`
				Payload string `json:"payload"`
				Hint    string `json:"hint"`
			}{
				Format:  session.Step.Display.Format,
				Payload: session.Step.Display.Payload,
				Hint:    session.Step.Display.Hint,
			}
		}
		for _, field := range session.Step.Fields {
			step.Fields = append(step.Fields, struct {
				Name  string `json:"name"`
				Kind  string `json:"kind"`
				Label string `json:"label"`
			}{Name: field.Name, Kind: field.Kind, Label: field.Label})
		}
		if session.Step.Approval != nil {
			step.Approval = &struct {
				Destination string `json:"destination"`
				Instruction string `json:"instruction"`
			}{
				Destination: session.Step.Approval.Destination,
				Instruction: session.Step.Approval.Instruction,
			}
		}
		if session.Step.Profile != nil {
			step.Profile = &struct {
				Methods     []string `json:"methods"`
				Options     []string `json:"options"`
				AllowCreate bool     `json:"allow_create"`
				Params      []struct {
					Name  string `json:"name"`
					Kind  string `json:"kind"`
					Label string `json:"label"`
				} `json:"params"`
			}{
				Methods:     session.Step.Profile.Methods,
				Options:     session.Step.Profile.Options,
				AllowCreate: session.Step.Profile.AllowCreate,
			}
			for _, param := range session.Step.Profile.Params {
				step.Profile.Params = append(step.Profile.Params, struct {
					Name  string `json:"name"`
					Kind  string `json:"kind"`
					Label string `json:"label"`
				}{Name: param.Name, Kind: param.Kind, Label: param.Label})
			}
		}
		view.Step = step
	}
	return view
}

// offlinePairBuild resolves one bot's local pairing settings from the config
// file the locked command already holds, so a reload cannot move them
// mid-bind.
func offlinePairBuild(
	cfg *config.Config, db *sqlite.DB, devices *whatsapp.Devices,
) func(scope, bot string, fullHistory bool) (whatsapp.PairOptions, error) {
	return func(scope, botName string, fullHistory bool) (whatsapp.PairOptions, error) {
		scopeCfg, ok := cfg.Scopes[scope]
		if !ok {
			return whatsapp.PairOptions{}, fmt.Errorf("unknown scope %q", scope)
		}
		bot, ok := scopeCfg.Bots[botName]
		if !ok {
			return whatsapp.PairOptions{}, fmt.Errorf(
				"bot %q not found in scope %q", botName, scope)
		}
		options, err := whatsappOptions(bot)
		if err != nil {
			return whatsapp.PairOptions{}, err
		}
		return whatsapp.PairOptions{
			Scope:          scope,
			Bot:            botName,
			Devices:        devices,
			Bindings:       db.Bindings(),
			Pairings:       db.Pairings(),
			Peers:          db.Peers(),
			Chats:          db.Chats(),
			Messages:       db.Messages(),
			History:        options.History,
			FullHistory:    fullHistory,
			Directory:      db.Directory(),
			ImportContacts: options.ImportContacts,
			Outbox:         db.Outbox(),
			Log:            botflota.DiscardLogger(),
			DepsLogLevel:   botflota.ParseLogLevel(cfg.LogLevelDeps),
			DeviceName:     cfg.DeviceName,
		}, nil
	}
}
