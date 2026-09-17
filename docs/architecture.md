# Architecture

Component boundaries, ports, and the lifecycle of a message. This document
describes intent; update it as the code exists.

## Ports and adapters

Two independent ports sit beside the config engine and persistence:

- `MessageChannel` — transport port. A channel is one implementation that
  reaches a messaging provider; the provider (WhatsApp) can have several
  channels, such as whatsmeow today or the official Cloud API later.
- `LLMProvider` — provider port for classification and reply realization.
  Cloud and local providers sit behind the same interface.

The config engine loads YAML into the domain model; the engine never sees
YAML. Persistence stores runtime state: device store, chats, sessions, and
messages.

A messaging provider and an LLM provider are different things. A bot always
needs a channel; an LLM is optional and only used when configured.

The YAML-free domain types and ports live in `internal/domain` (`ports.go` for
the ports, `records.go` for the persisted shapes), the graph engine in
`internal/engine`, the SQLite adapter in `internal/sqlite`, and the whatsmeow
channel in `internal/whatsapp`. The fleet, with its per-bot filter, lives in
`internal/fleet`; the outbox in `internal/outbox`.

Behavior types (`Graph`, `Action`, `Node`) still live in `internal/config`
beside the YAML loader, and the engine imports them. They move only when the
database becomes their source (ROADMAP Phase 15).

## Domain model

- Bot — identity (account) plus settings, the reference to one graph, and an
  optional reference to one llm (ADR 0017).
- Graph — state machine with root, fallback, timeout, a routing override
  (ADR 0015), and a map of nodes.
- Node — goal, policy, entry/exit actions, tools, and optional node-local
  context.
- Edge — transition with a required user intent, except the default
  (catch-all) edge. `triggers` is an optional deterministic fast path; LLM
  routing resolves input that matches no trigger.
- Action — ordered steps (`message`, `request`, `location`) with guards and
  declared `inputs`; exposed to the LLM when a node lists it in `tools`.
- Tool — an action the LLM may invoke with validated arguments; an input
  may bind to a node or graph variable (read and write).
- Node context — node variables that reset when the node is entered again.
- Visited nodes — ordered list of nodes visited (current last) used for
  routing and as LLM context; reordered on each visit, never reset by
  navigation.
- Graph context — session-wide variables that persist across nodes.
- Scope — the top-level config namespace holding its own actions, graphs, and
  bots (ADR 0016). A tenant is one use; the name is free. References and
  sessions never cross scopes.
- LLM — a named, scoped model profile (provider, model, credentials, routing
  defaults, bounds) referenced by a bot (ADR 0017).

## Message lifecycle

Inbound:

1. Channel receives an event, maps it to an `IncomingMessage`, and resolves
   the sender to a phone number; a message with no known phone number is
   dropped (ADR 0020).
2. The fleet routes the event to the bot; the bot's filter drops it (groups,
   allow/ignore lists) or passes it to the engine.
3. Graph engine loads the session, or creates one at the graph root. On
   creation the incoming message is evaluated before the root's entry
   actions: if it transitions, the root message is skipped; if not, the
   root's `on_enter` runs.
4. Deterministic matching runs over candidate nodes: visited nodes (oldest
   first) plus the current node last (ADR 0010). Edges with triggers are
   fuzzy matched; the best score wins and ties go to the later candidate.
   The default edge is only taken from the current node.
5. When the graph routing mode is `always`, or nothing matched and the mode
   is `fallback`, the tool loop starts (ADR 0008): the LLM may call node
   tools with arguments validated against each action's `inputs`, and may
   produce a reply.
6. Tool results return to the LLM as context; missing required arguments
   return a tool error and the LLM asks the user, storing the answer in the
   bound variable. The LLM can chain calls, stay in the node, or take a
   declared edge; it never invents a transition.
7. Engine runs the selected transition: exit actions, target node, entry
   actions.
8. Effective policy composes node policy (always) and message policy
   (optional refinement). Reply text is deterministic, LLM-realized, or both
   (anchor plus fallback); a policy check runs before anything is queued.
9. Results are queued in the bot's outbox and the session is persisted.

Outbound:

1. When read receipts are enabled, the bot waits `read_receipt_delay` and
   marks the inbound message read.
2. It waits `reply_delay`, showing a composing indicator when `simulate_typing`
   is on, then sends.
3. Outbox paces consecutive messages with `queue_delay`, one message at a time
   per bot; every delay adds a shared random `jitter` (ADR 0011).
4. Channel sends them; failures are surfaced, never silently dropped.

## Templates

Steps and messages may reference runtime values: `{{graph.*}}`, `{{node.*}}`,
`{{sender.*}}`, `{{message.*}}`, `{{bot.*}}`, tool arguments as
`{{input.<name>}}`, and request results as
`{{node.<request-name>.resp.body.*}}`,
`{{node.<request-name>.resp.headers.*}}`, `{{node.<request-name>.status}}`,
and `{{node.<request-name>.duration}}`. Tool result syntax is still open
(Phase 4).

`graph` and `node` are read-write; `sender`, `message`, `bot`, and `input` are
read-only, and `node` resets on every node entry. A missing path, an unknown
root, and a whole map or slice are errors, never empty or literal text; a
literal `{{` is written `\{{` (ADR 0018). The context and the resolver both
live in `internal/template`.

The engine resolves only runtime conversation values here. Secrets are not a
template context: config strings interpolate `${ENV_VAR}` once at load
(ADR 0014).

## Async requests

APIs that answer asynchronously (202 plus a status endpoint) are polled
within bounds: the LLM calls the operation, then checks its status up to its
tool budget; longer waits continue on later messages. A deterministic polling
step (interval, timeout, success condition) is an open decision for Phase 4/8.

## Chats and sessions

A chat is the durable thread with one sender, keyed by (scope, bot, sender);
`sender` is the resolved phone number in plain digits (ADR 0020). It never
ends and gathers every message. A session is one bounded run of the graph for
a chat: it starts at the root and expires after the graph timeout (or
`default_timeout`). An expired session is replaced by a new run; the next
message starts at root. Chat, session, and message history live in SQLite
(ADR 0026).

The session also keeps the visited nodes (ADR 0010): the ordered list of
nodes used for routing and handed to the LLM as context, bounded to a
configured maximum.

## Concurrency

- One actor per bot: inbound events and sessions for a bot are serialized;
  different bots run concurrently.
- One WhatsApp phone number belongs to one Botflota bot. Multiple linked
  companion devices for the same number would receive the same traffic and can
  produce duplicate replies.
- The tool loop runs inside the bot actor and is bounded (max tools per
  message, timeout); a slow provider cannot stall other bots.
- Bounded queues between the channel, the engine, and the outbox provide
  backpressure.
- No shared mutable state between bots; shared services are read-only or
  synchronized.
- Reconnect uses capped exponential backoff with jitter; a bot that is
  logged out or banned stops and reports instead of retrying (ADR 0011).
- A fleet supervisor starts, stops, and reports bot actors.

## Logging

One structured logger (`log/slog`) writes plain, column-aligned lines with no
ANSI escapes, so `journalctl` and `docker logs` show them verbatim:
`[time] LEVEL [pkg/file.go:line] message key=value`. `log_level` sets the level
for the application and `log_level_deps` for logs forwarded from dependencies
such as the channel; `off` silences a category. Levels follow one rule: debug
for
per-message detail, info for lifecycle and state, warn for recoverable
conditions, and error for failures. Message bodies are debug-only, and secrets
stay masked by type (ADR 0014).

## Config layout

A config file is global settings plus `scopes` (ADR 0016). Each scope holds
its own llms, actions, graphs, and bots. `include` pulls other files that use
the same shape; several files may extend one scope, paths resolve relative to
the including file, and a file loaded twice is skipped, so diamond includes
and cycles are safe. Global settings are only valid in the main file, and
scope is never derived from folders (ADR 0005). A relative `data_dir` resolves
against the directory of the config file, like include paths; an empty one
falls back to the XDG data home.

## Configuration pipeline

```
YAML -> strict decode -> ${ENV_VAR} expansion -> semantic validation -> engine
```

Environment interpolation runs on decoded strings, so secret characters
cannot break the YAML.

Runtime validation happens in Go only: strict YAML decoding rejects unknown
fields and wrong types, and semantic checks cover reference resolution,
scope isolation, uniqueness, reachability, and mode rules. The
`config.schema.json` file is generated from the Go types for editor support
and is never edited by hand (ADR 0007).

## Explicitly avoided for now

- Graph databases. SQLite tables and YAML are enough.
- Code generation from the schema in either direction.
- Behavior stored in the database before the dashboard exists.
- Shared mutable state between bots.
