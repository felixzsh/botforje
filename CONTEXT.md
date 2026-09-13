# BotForje — Context

## What it is

A declarative WhatsApp bot framework written in Go. Behavior lives in YAML:
bots, conversation graphs, actions, and guards. The engine runs a fleet of
WhatsApp accounts in a single process through the native Multi-Device
protocol (`go.mau.fi/whatsmeow`).

## Principles

- Declarative first: behavior lives in YAML; changing a bot never means
  changing the runtime.
- One runtime: a single binary, no mandatory auxiliary processes.
- The graph is the core: the LLM drives and enriches it, but never replaces
  it.
- Config can be authored by hand, by a UI, or AI-assisted; the runtime does
  not change for any of them.

## Runtime choice

Go and whatsmeow. Rationale and trade-offs: `docs/adr/0001-go-and-whatsmeow.md`.

## MVP definition

A config-driven fleet runner with these capabilities, without dashboard or
SaaS infrastructure:

- One instance runs multiple WhatsApp bots.
- Bots, graphs, and actions defined in YAML; YAML is the behavior source of
  truth.
- Graph conversations with sessions persisted in SQLite.
- Actions with `message`, `request`, and `location` steps; cooldown guards.
- Inbound filters (groups, allow/ignore senders) and a per-bot outbox with
  configurable delay.
- CLI: `validate`, `auth`, `daemon`, `status`, `reload`.
- REST API with key-based auth.
- Hybrid LLM support designed in from the start: deterministic routing works
  with no LLM configured; intents, providers, and agentic tools are
  first-class when enabled.

Deferred work and out-of-scope items: `PLAN.md`.

## Decisions

Settled decisions live in `docs/adr/`:

| ADR | Decision |
|---|---|
| 0001 | Go and whatsmeow as the runtime foundation |
| 0002 | YAML is the behavior source of truth; SQLite stores runtime state |
| 0003 | Hybrid intent model |
| 0004 | Messaging channel and LLM provider are separate ports |
| 0005 | Scope is an explicit entity field |
| 0006 | SQLite as the only persistence technology in the MVP |
| 0007 | Config schema generated from Go types; validation in Go |
| 0008 | Agentic nodes with parameterized tools |
| 0009 | Config reload semantics |
| 0010 | Node memory and routing across visited nodes |

## Current state

Pre-code. The config v1 shape is drafted in `config.example.yaml` and
`config.schema.json`; no implementation started.

## Glossary

- Bot — one WhatsApp account plus the graph that gives it behavior.
- Fleet — the set of bots a single BotForje instance runs.
- Scope — generic isolation namespace; references and sessions never cross
  scopes.
- Graph — conversation state machine owned by one bot; may override the
  instance routing mode.
- Node — a state: goal, policy, entry/exit actions, agent tools, and
  optional node-local context.
- Goal — the agent's objective in a node, short and clear.
- Edge — transition between nodes with a required user intent, except the
  default (catch-all) edge; triggers is an optional deterministic fast path.
- Intent — on an edge, the user trigger; on a message, the reply's goal.
  Short, clear text (not snake_case), like goal.
- Action — reusable pipeline of steps (`message`, `request`, `location`)
  with guards and optional declared inputs.
- Tool — an action a node exposes to the agent, invoked with validated
  arguments (`inputs`).
- Input — named, typed argument of a tool; may bind to a node or graph
  variable to read and write it.
- Node context — node variables; reset when the node is entered again.
- Graph context — session-wide variables; persist across nodes.
- Node memory — ordered list of visited nodes per session, current last; used
  for routing and as agent context.
- Agent loop — bounded sequence of tool calls and replies the LLM runs for
  one incoming message inside a node.
- Step — single operation inside an action.
- Guard — rule that limits execution, for example a cooldown per sender.
- Policy — constraints for agent decisions, tool selection, and generated
  text. Node policy is ambient; message policy refines it.
- Session — per-sender conversation state stored in SQLite.
- Channel — messaging transport implementation (WhatsApp today).
- Provider — LLM implementation (cloud or local) used for routing and replies.
- Inbox — routes inbound events to the right bot and session.
- Outbox — per-bot FIFO queue that paces outbound messages.
