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

// Options configures an Engine. Bot, Graph, Actions, Store, Chats, and Senders
// are required.
type Options struct {
	Bot            string
	Scope          string
	Graph          spec.Graph
	Actions        map[string]spec.Action
	Store          botflota.SessionStore
	Chats          botflota.ChatStore
	Senders        botflota.SenderStore
	Messages       botflota.MessageStore
	LLM            botflota.LLMProvider
	HTTPClient     *http.Client
	Now            func() time.Time
	Rand           *rand.Rand
	DefaultTimeout time.Duration
	Log            *slog.Logger
}

// Engine runs one graph for one bot. It is not safe for concurrent use; one
// bot actor owns it (docs/architecture.md).
type Engine struct {
	bot            string
	scope          string
	graph          spec.Graph
	actions        map[string]spec.Action
	store          botflota.SessionStore
	chats          botflota.ChatStore
	senders        botflota.SenderStore
	messages       botflota.MessageStore
	llm            botflota.LLMProvider
	hasLLM         bool
	http           *http.Client
	now            func() time.Time
	rand           *rand.Rand
	defaultTimeout time.Duration
	log            *slog.Logger
}

// New builds an Engine and fills in its optional collaborators.
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

// Handle processes one inbound message and returns the messages to deliver.
// It loads or creates the session, routes, runs the transition and its
// actions, and persists the session.
func (e *Engine) Handle(
	ctx context.Context, msg botflota.IncomingMessage,
) ([]botflota.OutgoingMessage, error) {
	if msg.Sender == "" {
		return nil, errors.New("engine: incoming message has no sender")
	}
	if e.messages == nil {
		return nil, errors.New("engine: no message store")
	}
	now := e.now()
	chat, sender, session, created, err := e.load(ctx, msg, now)
	if err != nil {
		return nil, err
	}
	r := &run{
		ctx:     ctx,
		engine:  e,
		session: session,
		chat:    chat,
		tctx:    e.contextOf(session, msg, sender),
		msg:     msg,
	}

	result, err := e.route(r, created)
	if err != nil {
		return nil, err
	}
	r.routing = result.Method
	switch {
	case result.Target == "":
		if created {
			e.log.Debug("entered root node", "node", session.Node)
			if err := r.enter(); err != nil {
				return nil, err
			}
		} else {
			e.log.Debug("no transition", "node", session.Node)
		}
	default:
		if err := r.move(result.Target, !created); err != nil {
			return nil, err
		}
	}

	session.GraphContext = r.tctx.Values(botflota.RootGraph)
	session.NodeContext = r.tctx.Values(botflota.RootNode)
	session.UpdatedAt = now
	session.ExpiresAt = e.expiry(now)
	if err := e.store.Save(ctx, session); err != nil {
		return nil, err
	}
	if err := e.recordMessages(ctx, chat.ID, session.ID, msg, r.routing, r.out, now); err != nil {
		return nil, err
	}
	if err := e.chats.RecordMessage(ctx, chat.ID, now); err != nil {
		return nil, err
	}
	if r.guardsDirty {
		if err := e.chats.SaveGuards(ctx, chat.ID, chat.Guards); err != nil {
			return nil, err
		}
	}
	return r.out, nil
}

// recordMessages stores the inbound message and every reply under the chat and
// the session run that handled them (ADR 0026). The session is saved first, so
// its id is set; an outbound reply keeps its kind, so a location stays
// distinguishable from text. Provenance travels with the records: the channel
// message id for dedup, and the routing that handled the run plus the origin
// of each reply.
func (e *Engine) recordMessages(
	ctx context.Context,
	chatID, sessionID int64,
	msg botflota.IncomingMessage,
	routing botflota.RoutingMethod,
	out []botflota.OutgoingMessage,
	now time.Time,
) error {
	inbound := &botflota.Message{
		ChatID:            chatID,
		SessionID:         sessionID,
		Direction:         botflota.Inbound,
		Kind:              messageKind(msg.Kind),
		Body:              msg.Body,
		At:                msg.At,
		ProviderMessageID: msg.ID,
		Ingest:            botflota.IngestLive,
		Routing:           routing,
	}
	if err := e.messages.Append(ctx, inbound); err != nil {
		return fmt.Errorf("engine: record inbound: %w", err)
	}
	for i := range out {
		sent := &botflota.Message{
			ChatID:    chatID,
			SessionID: sessionID,
			Direction: botflota.Outbound,
			Kind:      out[i].Kind,
			Body:      out[i].Body,
			At:        now,
			Ingest:    botflota.IngestLive,
			Origin:    out[i].Origin,
			Routing:   out[i].Routing,
		}
		if err := e.messages.Append(ctx, sent); err != nil {
			return fmt.Errorf("engine: record outbound: %w", err)
		}
		// The channel reports its message id after delivery; the fleet fills
		// the stored row with it so a later history sync deduplicates the
		// reply (ADR 0026, ADR 0035).
		out[i].RecordID = sent.ID
	}
	return nil
}

func (e *Engine) load(
	ctx context.Context, msg botflota.IncomingMessage, now time.Time,
) (*botflota.Chat, *botflota.Sender, *botflota.Session, bool, error) {
	if e.senders == nil {
		return nil, nil, nil, false, errors.New("engine: no sender store")
	}
	sender, _, err := e.senders.Upsert(ctx, e.scope, msg.Sender, msg.SenderName, now)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("engine: sender: %w", err)
	}
	chat, err := e.chats.Ensure(ctx, e.scope, e.bot, sender.ID, now)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("engine: chat: %w", err)
	}
	session, err := e.store.Latest(ctx, chat.ID)
	if err == nil && !session.Expired(now) {
		return chat, sender, session, false, nil
	}
	if err != nil && !errors.Is(err, botflota.ErrNotFound) {
		return nil, nil, nil, false, err
	}
	return chat, sender, e.start(chat.ID, now), true, nil
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

// expiry returns the session deadline, or the zero time when no timeout is
// configured (a zero deadline never expires).
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
		"phone": msg.Sender,
		"name":  sender.DisplayName,
	})
	_ = tctx.Reset(botflota.RootMessage, map[string]any{
		"body": msg.Body,
		"id":   msg.ID,
		"at":   formatAt(msg.At),
	})
	_ = tctx.Reset(botflota.RootBot, map[string]any{
		"id":    e.bot,
		"scope": e.scope,
	})
	return tctx
}

// run is the state of one Handle call. Routing is how this message was
// handled; every message the run persists carries it (ADR 0026).
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

// move runs the exit actions when asked, enters target, resets its node
// context, and reorders visited nodes so target is last (ADR 0010).
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

// enter runs the entry actions of the current node.
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

// action runs one action, applying its guards (ADR 0011).
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

// applyGuards checks an action's guards and records the run when they allow it.
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

// recordGuards stamps an allowed run in the chat so the next one is judged
// against it (ADR 0027).
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

// messageKind normalizes an unset kind to text: a channel that predates the
// kind vocabulary, or a plain text event, leaves it empty (ADR 0036).
func messageKind(kind botflota.MessageKind) botflota.MessageKind {
	if kind == "" {
		return botflota.KindText
	}
	return kind
}
