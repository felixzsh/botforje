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

	"github.com/felixzsh/botforje/internal/config"
	"github.com/felixzsh/botforje/internal/domain"
	"github.com/felixzsh/botforje/internal/logging"
	"github.com/felixzsh/botforje/internal/template"
)

// Options configures an Engine. Bot, Graph, Actions, Store, and Chats are
// required.
type Options struct {
	Bot            string
	Scope          string
	Graph          config.Graph
	Actions        map[string]config.Action
	Store          domain.SessionStore
	Chats          domain.ChatStore
	LLM            domain.LLMProvider
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
	graph          config.Graph
	actions        map[string]config.Action
	store          domain.SessionStore
	chats          domain.ChatStore
	llm            domain.LLMProvider
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
		llm:            opts.LLM,
		hasLLM:         opts.LLM != nil,
		http:           opts.HTTPClient,
		now:            opts.Now,
		rand:           opts.Rand,
		defaultTimeout: opts.DefaultTimeout,
		log:            opts.Log,
	}
	if e.log == nil {
		e.log = logging.Discard()
	}
	if e.llm == nil {
		e.llm = domain.NullLLM{}
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
	ctx context.Context, msg domain.IncomingMessage,
) ([]domain.OutgoingMessage, error) {
	if msg.Sender == "" {
		return nil, errors.New("engine: incoming message has no sender")
	}
	now := e.now()
	session, created, err := e.load(ctx, msg, now)
	if err != nil {
		return nil, err
	}
	r := &run{
		ctx:     ctx,
		engine:  e,
		session: session,
		tctx:    e.contextOf(session, msg),
		msg:     msg,
	}

	target, err := e.route(r, created)
	if err != nil {
		return nil, err
	}
	switch {
	case target == "":
		if created {
			e.log.Debug("entered root node", "node", session.Node)
			if err := r.enter(); err != nil {
				return nil, err
			}
		} else {
			e.log.Debug("no transition", "node", session.Node)
		}
	default:
		if err := r.move(target, !created); err != nil {
			return nil, err
		}
	}

	session.GraphContext = r.tctx.Values(template.RootGraph)
	session.NodeContext = r.tctx.Values(template.RootNode)
	session.UpdatedAt = now
	session.ExpiresAt = e.expiry(now)
	if err := e.store.Save(ctx, session); err != nil {
		return nil, err
	}
	return r.out, nil
}

func (e *Engine) load(
	ctx context.Context, msg domain.IncomingMessage, now time.Time,
) (*domain.Session, bool, error) {
	chat, err := e.chats.Ensure(ctx, e.scope, e.bot, msg.Sender, now)
	if err != nil {
		return nil, false, fmt.Errorf("engine: chat: %w", err)
	}
	session, err := e.store.Latest(ctx, chat.ID)
	if err == nil && !session.Expired(now) {
		return session, false, nil
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, false, err
	}
	return e.start(chat.ID, now), true, nil
}

func (e *Engine) start(chatID int64, now time.Time) *domain.Session {
	root := e.graph.Nodes[e.graph.Root]
	return &domain.Session{
		ChatID:       chatID,
		Node:         e.graph.Root,
		Memory:       []string{e.graph.Root},
		GraphContext: maps.Clone(e.graph.InitialContext),
		NodeContext:  maps.Clone(root.InitialContext),
		Cooldowns:    map[string]time.Time{},
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
	session *domain.Session, msg domain.IncomingMessage,
) *template.Context {
	tctx := template.New()
	_ = tctx.Reset(template.RootGraph, session.GraphContext)
	_ = tctx.Reset(template.RootNode, session.NodeContext)
	_ = tctx.Reset(template.RootSender, map[string]any{
		"phone": msg.Sender,
	})
	_ = tctx.Reset(template.RootMessage, map[string]any{
		"body": msg.Body,
		"id":   msg.ID,
		"at":   formatAt(msg.At),
	})
	_ = tctx.Reset(template.RootBot, map[string]any{
		"id":    e.bot,
		"scope": e.scope,
	})
	return tctx
}

// run is the state of one Handle call.
type run struct {
	ctx     context.Context
	engine  *Engine
	session *domain.Session
	tctx    *template.Context
	msg     domain.IncomingMessage
	out     []domain.OutgoingMessage
}

// move runs the exit actions when asked, enters target, resets its node
// context, and reorders node memory so target is last (ADR 0010).
func (r *run) move(target string, runExit bool) error {
	if runExit {
		if err := r.actions(r.engine.graph.Nodes[r.session.Node].OnExit); err != nil {
			return err
		}
	}
	r.engine.log.Debug("transition", "from", r.session.Node, "to", target, "exit", runExit)
	r.session.Node = target
	r.session.NodeContext = maps.Clone(r.engine.graph.Nodes[target].InitialContext)
	if err := r.tctx.Reset(template.RootNode, r.session.NodeContext); err != nil {
		return err
	}
	r.session.Memory = reorder(r.session.Memory, target)
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

// action runs one action, applying its cooldown guard (ADR 0011).
func (r *run) action(name string) error {
	action, ok := r.engine.actions[name]
	if !ok {
		return fmt.Errorf("engine: unknown action %q", name)
	}
	if guards := action.Guards; guards != nil && guards.Cooldown != nil {
		guard := guards.Cooldown
		if r.blocked(name, guard.Duration) {
			return r.steps(guard.OnBlocked, name+".on_blocked")
		}
		if r.session.Cooldowns == nil {
			r.session.Cooldowns = map[string]time.Time{}
		}
		r.session.Cooldowns[name] = r.engine.now()
	}
	return r.steps(action.Steps, name)
}

func (r *run) blocked(name string, seconds int) bool {
	last, ok := r.session.Cooldowns[name]
	if !ok {
		return false
	}
	return r.engine.now().Sub(last) < time.Duration(seconds)*time.Second
}

func reorder(memory []string, node string) []string {
	out := make([]string, 0, len(memory)+1)
	for _, name := range memory {
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
