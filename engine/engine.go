// Package engine runs conversation graphs with no channel attached: it
// consumes an incoming message and returns the messages to deliver. Channels
// and LLM providers are ports (ADR 0004).
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand"
	"net/http"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// Options configures an Engine. Bot, Graph, Actions, Store, Chats, Senders,
// Messages, and Turn are required.
type Options struct {
	Bot      string
	Scope    string
	Graph    spec.Graph
	Actions  map[string]spec.Action
	Store    botflota.SessionStore
	Chats    botflota.ChatStore
	Senders  botflota.SenderStore
	Messages botflota.MessageStore
	Turn     botflota.TurnStore
	// Notify receives the first-contact hook; nil means no consumer (ADR 0045).
	Notify         botflota.FirstContactNotifier
	LLM            botflota.LLMProvider
	HTTPClient     *http.Client
	Now            func() time.Time
	Rand           *rand.Rand
	DefaultTimeout time.Duration
	Log            *slog.Logger
}

// Engine is not safe for concurrent use; one bot actor owns it
// (docs/architecture.md).
type Engine struct {
	bot            string
	scope          string
	graph          spec.Graph
	actions        map[string]spec.Action
	store          botflota.SessionStore
	chats          botflota.ChatStore
	senders        botflota.SenderStore
	messages       botflota.MessageStore
	turn           botflota.TurnStore
	notify         botflota.FirstContactNotifier
	llm            botflota.LLMProvider
	hasLLM         bool
	http           *http.Client
	now            func() time.Time
	rand           *rand.Rand
	defaultTimeout time.Duration
	log            *slog.Logger
}

func New(opts Options) *Engine {
	e := &Engine{
		bot:            opts.Bot,
		scope:          opts.Scope,
		graph:          opts.Graph,
		actions:        opts.Actions,
		store:          opts.Store,
		chats:          opts.Chats,
		senders:        opts.Senders,
		messages:       opts.Messages,
		turn:           opts.Turn,
		notify:         opts.Notify,
		llm:            opts.LLM,
		hasLLM:         opts.LLM != nil,
		http:           opts.HTTPClient,
		now:            opts.Now,
		rand:           opts.Rand,
		defaultTimeout: opts.DefaultTimeout,
		log:            opts.Log,
	}
	if e.log == nil {
		e.log = botflota.DiscardLogger()
	}
	if e.llm == nil {
		e.llm = botflota.NullLLM{}
	}
	if e.http == nil {
		e.http = http.DefaultClient
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.rand == nil {
		e.rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return e
}

func (e *Engine) Handle(
	ctx context.Context, msg botflota.IncomingMessage,
) ([]botflota.OutgoingMessage, error) {
	if msg.SenderPhone == "" {
		return nil, errors.New("engine: incoming message has no sender")
	}
	if msg.Content == nil {
		// A channel that predates the kind vocabulary, or a plain text event,
		// leaves the payload empty.
		msg.Content = botflota.TextContent{}
	}
	if e.messages == nil {
		return nil, errors.New("engine: no message store")
	}
	if e.turn == nil {
		return nil, errors.New("engine: no turn store")
	}
	// Dedup runs before load: a redelivery reads the sender and chat but
	// never writes them, so it cannot move last_seen or create the rows it
	// is about to ignore (ADR 0040).
	seen, err := e.storedBefore(ctx, msg)
	if err != nil {
		return nil, err
	}
	if seen {
		e.log.Debug("duplicate message ignored", "id", msg.ProviderMessageID)
		return nil, nil
	}
	now := e.now()
	current, err := e.load(ctx, msg, now)
	if err != nil {
		return nil, err
	}
	if current.newSender {
		e.notifyFirstContact(ctx, current.sender, now)
	}
	r := &run{
		ctx:     ctx,
		engine:  e,
		session: current.session,
		chat:    current.chat,
		tctx:    e.contextOf(current.session, msg, current.sender),
		msg:     msg,
	}

	result, err := e.route(r, current.newSession)
	if err != nil {
		return nil, err
	}
	r.routing = result.Method
	switch {
	case result.Target == "":
		if current.newSession {
			e.log.Debug("entered root node", "node", current.session.Node)
			if err := r.enter(); err != nil {
				return nil, err
			}
		} else {
			e.log.Debug("no transition", "node", current.session.Node)
		}
	default:
		if err := r.move(result.Target, !current.newSession); err != nil {
			return nil, err
		}
	}

	session := current.session
	session.GraphContext = r.tctx.Values(botflota.RootGraph)
	session.NodeContext = r.tctx.Values(botflota.RootNode)
	session.UpdatedAt = now
	session.ExpiresAt = e.expiry(now)
	if err := e.commitTurn(ctx, current.chat, session, msg, r.routing, r.out,
		now, r.guardsDirty); err != nil {
		return nil, err
	}
	return r.out, nil
}

// storedBefore reports a message the chat already stores. It reads the
// sender and chat without creating or updating them: the engine checks
// before load, so a redelivery never re-runs an action or an HTTP side
// effect and never moves last_seen (ADR 0026, ADR 0040). A chat that does
// not exist yet has no stored message, so the missing rows answer no.
func (e *Engine) storedBefore(
	ctx context.Context, msg botflota.IncomingMessage,
) (bool, error) {
	if msg.ProviderMessageID == "" {
		return false, nil
	}
	if e.senders == nil {
		// Nothing can be stored; load reports the missing store.
		return false, nil
	}
	sender, err := e.senders.Get(ctx, e.scope, msg.SenderPhone)
	if errors.Is(err, botflota.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("engine: sender lookup: %w", err)
	}
	chat, err := e.chats.ForSender(ctx, e.scope, e.bot, sender.ID)
	if errors.Is(err, botflota.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("engine: chat lookup: %w", err)
	}
	seen, err := e.messages.HasProviderID(ctx, chat.ID, msg.ProviderMessageID)
	if err != nil {
		return false, fmt.Errorf("engine: dedup: %w", err)
	}
	return seen, nil
}

// RecordSuppressed stores an inbound message for a chat handed to a human:
// history and deduplication, no routing and no session change (ADR 0043). A
// redelivery whose id the chat already stores is dropped like any other.
func (e *Engine) RecordSuppressed(
	ctx context.Context, msg botflota.IncomingMessage,
) error {
	if msg.SenderPhone == "" {
		return errors.New("engine: incoming message has no sender")
	}
	if msg.Content == nil {
		msg.Content = botflota.TextContent{}
	}
	seen, err := e.storedBefore(ctx, msg)
	if err != nil {
		return err
	}
	if seen {
		return nil
	}
	now := e.now()
	current, err := e.load(ctx, msg, now)
	if err != nil {
		return err
	}
	if current.newSender {
		e.notifyFirstContact(ctx, current.sender, now)
	}
	record := &botflota.Message{
		ChatID:            current.chat.ID,
		Direction:         botflota.Inbound,
		Content:           msg.Content,
		At:                msg.At,
		ProviderMessageID: msg.ProviderMessageID,
		Ingest:            botflota.IngestLive,
		Routing:           botflota.RoutingUnknown,
	}
	if err := e.turn.Record(ctx, record, now); err != nil {
		return fmt.Errorf("engine: record suppressed inbound: %w", err)
	}
	return nil
}

// notifyFirstContact reports a profile the upsert just created, so a consumer
// learns the first contact without polling (ADR 0029, ADR 0045). It runs once
// the profile is durable and never fails the turn: the sender row itself is
// already stored, and a lost enqueue is logged.
func (e *Engine) notifyFirstContact(
	ctx context.Context, sender *botflota.Sender, at time.Time,
) {
	if e.notify == nil || sender == nil {
		return
	}
	event := botflota.FirstContact{
		Scope: e.scope, Bot: e.bot, Sender: *sender, At: at,
	}
	if err := e.notify.FirstContact(ctx, event); err != nil {
		e.log.Error("first-contact hook failed", "sender", sender.Phone, "error", err)
	}
}

// RecordOperator stores an outbound message the operator sent outside Botflota
// under its chat, with the operator origin, and queues nothing (ADR 0043). A
// provider id the chat already stores — a later history sync echoing it — is
// ignored.
func (e *Engine) RecordOperator(
	ctx context.Context, msg botflota.OutboundMessage,
) error {
	if msg.RecipientPhone == "" {
		return errors.New("engine: outbound message has no recipient")
	}
	if msg.Content == nil {
		msg.Content = botflota.TextContent{}
	}
	if e.senders == nil {
		return errors.New("engine: no sender store")
	}
	now := e.now()
	sender, _, err := e.senders.Upsert(ctx, e.scope, msg.RecipientPhone, "", now)
	if err != nil {
		return fmt.Errorf("engine: sender: %w", err)
	}
	chat, err := e.chats.Ensure(ctx, e.scope, e.bot, sender.ID, now)
	if err != nil {
		return fmt.Errorf("engine: chat: %w", err)
	}
	record := &botflota.Message{
		ChatID:            chat.ID,
		Direction:         botflota.Outbound,
		Content:           msg.Content,
		At:                msg.At,
		ProviderMessageID: msg.ProviderMessageID,
		Ingest:            botflota.IngestLive,
		Origin:            botflota.OriginOperator,
		Routing:           botflota.RoutingUnknown,
	}
	err = e.turn.Record(ctx, record, now)
	if errors.Is(err, botflota.ErrDuplicate) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("engine: record operator outbound: %w", err)
	}
	return nil
}

// commitTurn lands the whole turn in one unit: the session, the inbound
// message, every reply, the chat activity, and the guard state. The engine
// never writes those shapes directly, so a failure can never leave the
// session advanced with lost replies (ADR 0026, ADR 0027).
func (e *Engine) commitTurn(
	ctx context.Context,
	chat *botflota.Chat,
	session *botflota.Session,
	msg botflota.IncomingMessage,
	routing botflota.RoutingMethod,
	out []botflota.OutgoingMessage,
	now time.Time,
	guardsDirty bool,
) error {
	turn := botflota.TurnRecord{
		Scope:   e.scope,
		Bot:     e.bot,
		Session: session,
		ChatID:  chat.ID,
		At:      now,
		Inbound: &botflota.Message{
			ChatID:            chat.ID,
			Direction:         botflota.Inbound,
			Content:           msg.Content,
			At:                msg.At,
			ProviderMessageID: msg.ProviderMessageID,
			Ingest:            botflota.IngestLive,
			Routing:           routing,
		},
		Outbound: make([]botflota.OutboundRecord, 0, len(out)),
	}
	for i := range out {
		turn.Outbound = append(turn.Outbound, botflota.OutboundRecord{
			Recipient: out[i].RecipientPhone,
			Message: &botflota.Message{
				ChatID:    chat.ID,
				Direction: botflota.Outbound,
				Content:   out[i].Content,
				At:        now,
				Ingest:    botflota.IngestLive,
				Origin:    out[i].Origin,
				Routing:   out[i].Routing,
			},
		})
	}
	if guardsDirty {
		turn.Guards = chat.Guards
	}
	if err := e.turn.CommitTurn(ctx, turn); err != nil {
		return fmt.Errorf("engine: commit turn: %w", err)
	}
	for i := range out {
		out[i].StoredMessageID = turn.Outbound[i].Message.ID
	}
	return nil
}

// loaded is what one inbound message needs before routing: the durable chat
// and sender profile, the session run it continues or starts, and whether the
// message created the profile (the first-contact hook) or the run (ADR 0029,
// ADR 0045).
type loaded struct {
	chat       *botflota.Chat
	sender     *botflota.Sender
	session    *botflota.Session
	newSession bool
	newSender  bool
}

func (e *Engine) load(
	ctx context.Context, msg botflota.IncomingMessage, now time.Time,
) (loaded, error) {
	if e.senders == nil {
		return loaded{}, errors.New("engine: no sender store")
	}
	sender, created, err := e.senders.Upsert(ctx, e.scope, msg.SenderPhone,
		msg.SenderDisplayName, now)
	if err != nil {
		return loaded{}, fmt.Errorf("engine: sender: %w", err)
	}
	chat, err := e.chats.Ensure(ctx, e.scope, e.bot, sender.ID, now)
	if err != nil {
		return loaded{}, fmt.Errorf("engine: chat: %w", err)
	}
	session, err := e.store.Latest(ctx, chat.ID)
	if err == nil && !session.Expired(now) {
		return loaded{
			chat: chat, sender: sender, session: session, newSender: created,
		}, nil
	}
	if err != nil && !errors.Is(err, botflota.ErrNotFound) {
		return loaded{}, err
	}
	return loaded{
		chat: chat, sender: sender, session: e.start(chat.ID, now),
		newSession: true, newSender: created,
	}, nil
}

func (e *Engine) start(chatID int64, now time.Time) *botflota.Session {
	root := e.graph.Nodes[e.graph.Root]
	return &botflota.Session{
		ChatID:       chatID,
		Node:         e.graph.Root,
		VisitedNodes: []string{e.graph.Root},
		GraphContext: maps.Clone(e.graph.InitialContext),
		NodeContext:  maps.Clone(root.InitialContext),
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    e.expiry(now),
	}
}

func (e *Engine) sessionTimeout() time.Duration {
	if e.graph.Timeout > 0 {
		return time.Duration(e.graph.Timeout) * time.Second
	}
	return e.defaultTimeout
}

func (e *Engine) expiry(now time.Time) time.Time {
	if timeout := e.sessionTimeout(); timeout > 0 {
		return now.Add(timeout)
	}
	return time.Time{}
}

func (e *Engine) contextOf(
	session *botflota.Session, msg botflota.IncomingMessage, sender *botflota.Sender,
) *templateContext {
	tctx := newtemplateContext()
	// The roots seeded here are constants, so Reset cannot reject them.
	_ = tctx.Reset(botflota.RootGraph, session.GraphContext)
	_ = tctx.Reset(botflota.RootNode, session.NodeContext)
	_ = tctx.Reset(botflota.RootSender, map[string]any{
		"phone": msg.SenderPhone,
		"name":  sender.DisplayName,
	})
	_ = tctx.Reset(botflota.RootMessage, map[string]any{
		"body": msg.Content.VisibleText(),
		"id":   msg.ProviderMessageID,
		"at":   formatAt(msg.At),
	})
	_ = tctx.Reset(botflota.RootBot, map[string]any{
		"id":    e.bot,
		"scope": e.scope,
	})
	return tctx
}

type run struct {
	ctx         context.Context
	engine      *Engine
	session     *botflota.Session
	chat        *botflota.Chat
	tctx        *templateContext
	msg         botflota.IncomingMessage
	out         []botflota.OutgoingMessage
	routing     botflota.RoutingMethod
	guardsDirty bool
}

func (r *run) move(target string, runExit bool) error {
	if runExit {
		if err := r.actions(r.engine.graph.Nodes[r.session.Node].OnExit); err != nil {
			return err
		}
	}
	r.engine.log.Debug("transition", "from", r.session.Node, "to", target, "exit", runExit)
	r.session.Node = target
	r.session.NodeContext = maps.Clone(r.engine.graph.Nodes[target].InitialContext)
	if err := r.tctx.Reset(botflota.RootNode, r.session.NodeContext); err != nil {
		return err
	}
	r.session.VisitedNodes = reorder(r.session.VisitedNodes, target)
	return r.enter()
}

func (r *run) enter() error {
	return r.actions(r.engine.graph.Nodes[r.session.Node].OnEnter)
}

func (r *run) actions(names []string) error {
	for _, name := range names {
		if err := r.action(name); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) action(name string) error {
	action, ok := r.engine.actions[name]
	if !ok {
		return fmt.Errorf("engine: unknown action %q", name)
	}
	if blocked, err := r.applyGuards(name, action.Guards); blocked || err != nil {
		return err
	}
	return r.steps(action.Steps, name)
}

// A blocking guard reports true and has already run its on_blocked steps.
func (r *run) applyGuards(name string, guards *spec.Guards) (bool, error) {
	if guards == nil {
		return false, nil
	}
	state := r.chat.Guards[name]
	now := r.engine.now()
	if cd := guards.Cooldown; cd != nil && state.Cooldown != nil {
		if now.Sub(state.Cooldown.LastRun) < time.Duration(cd.Duration)*time.Second {
			return true, r.steps(cd.OnBlocked, name+".cooldown")
		}
	}
	var window time.Duration
	if rl := guards.RateLimit; rl != nil {
		window = time.Duration(rl.Window) * time.Second
		if lim := state.RateLimit; lim != nil && !lim.WindowStart.IsZero() &&
			now.Sub(lim.WindowStart) < window && lim.Count >= rl.Limit {
			return true, r.steps(rl.OnBlocked, name+".rate_limit")
		}
	}
	r.recordGuards(name, guards, state, now, window)
	return false, nil
}

func (r *run) recordGuards(
	name string, guards *spec.Guards, state botflota.GuardState,
	now time.Time, window time.Duration,
) {
	if guards.Cooldown != nil {
		state.Cooldown = &botflota.CooldownState{LastRun: now}
	}
	if guards.RateLimit != nil {
		count := 0
		if lim := state.RateLimit; lim != nil && now.Sub(lim.WindowStart) < window {
			count = lim.Count
		}
		state.RateLimit = &botflota.RateLimitState{WindowStart: now, Count: count + 1}
	}
	if r.chat.Guards == nil {
		r.chat.Guards = map[string]botflota.GuardState{}
	}
	r.chat.Guards[name] = state
	r.guardsDirty = true
}

func reorder(visited []string, node string) []string {
	out := make([]string, 0, len(visited)+1)
	for _, name := range visited {
		if name != node {
			out = append(out, name)
		}
	}
	return append(out, node)
}

func formatAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
