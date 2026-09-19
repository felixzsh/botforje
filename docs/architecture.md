# Architecture

Component boundaries, ports, and the lifecycle of a message. This document
describes intent; update it as the code exists.

## Ports and adapters

Three independent ports sit beside the config engine and persistence:

- `MessageChannel` — transport port. A channel is one implementation that
  reaches a messaging provider; the provider (WhatsApp) can have several
  channels, such as whatsmeow today or the official Cloud API later.
- `SystemOneProvider` — System One port for routing, policy, tool selection,
  and value mapping: typed answers (a choice from a closed set, a score, or a
  probability) with confidence and no generated text. Jev (TypeSafe) is the
  first adapter (ADR 0030).
- `LLMProvider` — model port for reply realization and the tool loop. Cloud
  and local providers sit behind the same interface.

The config engine loads YAML into the domain model; the engine never sees
YAML. Persistence stores runtime state: device store, chats, sessions, and
messages.

A messaging provider, a System One model, and an LLM are different things. A bot always
needs a channel; a System One model and an LLM are each optional and independent. A
generative LLM never decides a transition (ADR 0030).

The YAML-free core types and ports live in the root package `botflota`
(`ports.go` for the ports, `records.go` for the persisted shapes,
`logging.go` for the structured logger, `template.go` for the template
language). The declarative objects (`Graph`, `Node`, `Action`, `Bot`, and
model profiles) live in `spec`; `config` loads, interpolates, and validates
documents against them, including their template references
(`config/template_validation.go`); `engine` executes them and owns the runtime
context (`context.go`). The fleet, with its per-bot filter, lives in `fleet`;
the outbox is an unexported part of that package. Concrete integrations are
separate: `adapters/sqlite` implements the store ports and
`adapters/whatsapp` implements `MessageChannel`.

Behavior types (`Graph`, `Action`, `Node`) live in `spec`; a store may build
them directly when the database becomes their source (ROADMAP Phase 15).

## Package layout

A product capability gets its own package; a subordinate detail lives inside
its owner or, as a last resort, in `internal/` (ADR 0034).

- `botflota` (root) — core contracts: messages, persisted records, ports,
  errors, logging, and the template language; standard library only, never
  YAML, SQL, whatsmeow, or an LLM SDK.
- `spec` — declarative objects: scopes, graphs, actions, bots, model
  profiles, and the `Text` and `Secret` value types.
- `config` — documents: load, includes, interpolation, origin, validation,
  and template reference checks.
- `engine` — execution of `spec` and the runtime context; `fleet` — one actor
  per bot.
- `api` — REST handlers and routes; `fleet run` injects the live fleet and
  the stores.
- `adapters/<technology>` — concrete implementations of the core ports; an
  adapter may import the core, never the reverse.
- `cmd/botflota` — argument parsing and assembly.
- `internal/` — only a shared implementation detail that is not a capability
  and has no owner; nothing lives there today.

Dependencies point along `config → spec` and `engine → spec`; neither `config`
nor `engine` imports the other, and both use the core template language
(ADR 0034).

Planned capabilities land as `dashboard` (embedded UI), `auth` (authority and
credentials), `webhook` (outbound delivery), `behavior`
(`Graph`, `Action`, and `Node` once the database is their source), and one
package per channel or model provider under `adapters/`.

`fleet run` is the composition root: it loads config, opens stores and channel
devices, builds the fleet, and gives those live dependencies to `api`. API
handlers start with the smallest direct operation; a narrow service is
extracted only when a second real caller needs the same logic. There is no
generic `control` facade.

## Domain model

- Bot — identity (account) plus settings, the reference to one graph, and
  optional references to one system_one and one llm (ADR 0031).
- Sender — the durable profile of one person who writes to bots in a scope:
  phone, push name, and first/last seen, keyed by (scope, phone). The
  bot-person thread is the chat; a person may write to several bots of the same
  scope (ADR 0029).
- Graph — state machine with root, fallback, timeout, a routing override
  (ADR 0030), and a map of nodes.
- Node — goal, policy, entry/exit actions, tools, and optional node-local
  context.
- Edge — transition with a required user intent, except the default
  (catch-all) edge. `triggers` is an optional deterministic fast path;
  System One resolves input that matches no trigger (ADR 0030).
- Action — ordered steps (`message`, `request`, `location`) with guards and
  declared `inputs`; exposed to the LLM when a node lists it in `tools`.
- Tool — an action the LLM may invoke with validated arguments; an input
  may bind to a node or graph variable (read and write).
- Node context — node variables that reset when the node is entered again.
- Visited nodes — ordered list of nodes visited (current last) used for
  routing and as LLM context; reordered on each visit, never reset by
  navigation.
- Graph context — session-wide variables that persist across nodes.
- Scope — the top-level config namespace holding its own system_one, llms, actions,
  graphs, and bots (ADR 0016, ADR 0031). A tenant is one use; the name is
  free. References and sessions never cross scopes.
- Shared catalog — the instance-level `system_one` and `llms` a host provides, in
  the main config file only; a bot resolves a name in its scope first, then in
  `shared`. It is not a scope (ADR 0031).
- System One — a named model profile that answers typed decisions with confidence for
  routing and policy, referenced by a bot with `system_one`; it resolves in the scope
  or in `shared` (ADR 0030, ADR 0031).
- LLM — a named model profile (provider, model, credentials, realization
  defaults, bounds) referenced by a bot with `llm`; it resolves in the scope
  or in `shared` (ADR 0031).

## Message lifecycle

Inbound:

1. Channel receives an event, maps it to an `IncomingMessage`, and resolves
   the sender to a phone number; a message with no known phone number is
   dropped (ADR 0020).
2. The fleet routes the event to the bot; the bot's filter drops it (groups,
   allow/ignore lists) or passes it to the engine, which upserts the sender
   profile before routing (ADR 0029).
3. Graph engine loads the session, or creates one at the graph root. On
   creation the incoming message is evaluated before the root's entry
   actions: if it transitions, the root message is skipped; if not, the
   root's `on_enter` runs.
4. Deterministic matching runs over candidate nodes: visited nodes (oldest
   first) plus the current node last (ADR 0010). Edges with triggers are
   fuzzy matched; the best score wins and ties go to the later candidate.
   The default edge is only taken from the current node.
5. When the graph routing mode is `always`, or nothing matched and the mode
   is `fallback`, System One decides: one Choice question over the candidate
   edge intents, checked against the graph's confidence threshold. Below it
   the graph takes the default edge or escalates; the LLM never classifies
   (ADR 0030).
6. A node with tools handles the message through the pipeline (ADR 0033):
   System One decides, the LLM reads free text, and code applies. The node
   stays unless an exit edge is taken. The LLM loop (ADR 0033) is the opt-in
   alternative for calls a pipeline cannot bound.
7. Tool results feed the next step; a missing required argument is a tool
   error that asks the user, storing the answer in the bound variable.
   Neither model invents a transition; edges are the only exits.
8. Engine runs the selected transition: exit actions, target node, entry
   actions. Entry actions produce the prescribed reply, so a bot with no `llm`
   needs no provider (ADR 0032).
9. Effective policy composes node policy (always) and message policy
   (optional refinement). Reply text is prescribed, LLM-realized, or both
   (anchor plus fallback); a policy check runs before anything is queued.
10. Results are queued in the bot's outbox and the session is persisted.

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
`{{sender.phone}}` and `{{sender.name}}`, `{{message.*}}`, `{{bot.*}}`, tool
arguments as `{{input.<name>}}`, and request results as
`{{node.<request-name>.resp.body.*}}`,
`{{node.<request-name>.resp.headers.*}}`, `{{node.<request-name>.status}}`,
and `{{node.<request-name>.duration}}`. Tool result syntax is still open
(Phase 4).

`graph` and `node` are read-write; `sender`, `message`, `bot`, and `input` are
read-only, and `node` resets on every node entry. A missing path, an unknown
root, and a whole map or slice are errors, never empty or literal text; a
literal `{{` is written `\{{` (ADR 0018). The language lives in the core
(`template.go`), the runtime context in `engine` (`context.go`), and the
reference checks in `config` (`config/template_validation.go`) (ADR 0034).

The language resolves only runtime conversation values. `${ENV_VAR}` is a
separate secret mechanism, allowed only in model keys and request header
values, and it never combines with `{{...}}` (ADR 0014).

## Reply strategies

Three ways a reply reaches the queue, in order of preference (ADR 0032):

- Prescribed: a `body` message, optionally a list of variants chosen at
  random. The default, and the only one that needs no provider.
- Decision-selected: the System One model routes to a node whose prescribed body fits the
  intent or tone. The reply is still prescribed.
- Realized: the opt-in generative path; `body` plus `intent` anchors the model
  and doubles as the fallback. Intent-only, with no anchor, is discouraged.

## Tool pipeline

A node with tools handles an inbound message in place; the LLM loop is the
opt-in alternative for calls a pipeline cannot bound (ADR 0033). The exact
sequence below is the Phase 8 design.

1. Intake gate (System One): is the message actionable and clear for this
   node? The answers choose among act, ask and stay, delegate to extraction,
   or escalate.
2. Extraction (LLM): candidate parameters as literal values and spans, never
   final identifiers, validated against the tool `inputs`.
3. Selection (System One): one `Noul` per tool for multi-select plus an
   explicit `none`, or a `Choice` when only one can apply. Confidence below
   the threshold asks instead of acting.
4. Mapping (System One): each value maps to a real catalog entry with a
   `Choice`, in stages when the catalog exceeds the cardinality limit. Code
   does no matching of values.
5. Verification (System One): whether the lines reflect the message; a
   signal, not a proof.
6. Application (code): numbers and dates parsed from the literal span,
   invariants checked, the tool applied, and a prescribed confirmation sent.

A handled message takes no edge and does not re-run entry actions, so a tool
node serves many messages. Edges remain the only transitions, and a tool node
declares its exits (`done`, `cancel`, human) instead of a catch-all default.
Side effects wait for the confirmation.

## Async requests

APIs that answer asynchronously (202 plus a status endpoint) are polled
within bounds: the LLM calls the operation, then checks its status up to its
tool budget; longer waits continue on later messages. A deterministic polling
step (interval, timeout, success condition) is an open decision for Phase 4/8.

## Chats and sessions

A chat is the durable thread with one sender, keyed by (scope, bot, sender);
`sender` is the resolved phone number in plain digits (ADR 0020) and the chat
references its profile (ADR 0029). It never ends and gathers every message. A
session is one bounded run of the graph for a chat: it starts at the root and
expires after the graph timeout (or `default_timeout`). An expired session is
replaced by a new run; the next message starts at root. Chat, session, and
message history live in SQLite (ADR 0026).

The session also keeps the visited nodes (ADR 0010): the ordered list of
nodes used for routing and handed to the LLM as context, bounded to a
configured maximum.

## REST API

`fleet run` hosts the read-only API in the same process when `port` is set;
`port: 0` keeps the API off and the address defaults to `127.0.0.1`. The
routes live under `/api/v1`: `status`, `bots`, `chats`, `sessions`,
`messages`, and `config`. Every addressable object carries its scope, a
message carries its bot as a (scope, bot) pair, and a chat is addressed by that
pair plus the sender's phone number. Errors use one shape,
`{"error":{"code","message"}}`.

Authentication is the instance admin key: created on first run, stored as a
hash in SQLite, and written once to `admin.key` under `data_dir` with mode
0600 for the CLI; `fleet run --new-admin-key` regenerates it. Every request
carries `Authorization: Bearer` and resolves one principal behind a single
seam, so scoped authority (Phase 13) replaces the resolver. `config` serves
the typed model with secrets masked (ADR 0014). Handlers stay small and
direct; a narrow service is extracted only when a second real caller shares
the operation.

## Pairing

Linking a bot to WhatsApp is a separate step from running it: `bot pair` opens a
channel, shows the QR code or the pairing code, and stores the resulting
credentials in the shared SQLite file (ADR 0006). Two key systems are involved,
and only one is software: the companion identity (Noise and Signal keys, prekeys,
registration id) lives in the device store and can run on a server, while a
passkey lives in an authenticator on the account owner's device and never leaves
it (ADR 0028).

When the server requires a passkey, the phone performs the ceremony and the
companion receives the handoff; if the server asks the companion instead, the
pending step is reported and the bot stays stopped (ADR 0028).

## Concurrency

- One actor per bot: inbound events and sessions for a bot are serialized;
  different bots run concurrently.
- The engine and the outbox are independent, each serialized; a bot keeps
  reading inbound while its outbox paces earlier replies (ADR 0011).
- Config hot reload watches the main file and its includes and is
  all-or-nothing; a restart-required change is logged, never silent (ADR 0009).
- One WhatsApp phone number belongs to one Botflota bot. Multiple linked
  companion devices for the same number would receive the same traffic and can
  produce duplicate replies.
- The tool loop runs inside the bot actor and is bounded (max tools per
  message, timeout); a slow provider cannot stall other bots.
- Bounded queues between the channel, the engine, and the outbox provide
  backpressure.
- No shared mutable state between bots; shared services are read-only or
  synchronized.
- Reconnect uses capped exponential backoff with jitter; a bot that is logged
  out, banned, or rejected with 428 (passkey required) or 463 (reachout
  timelock) stops and reports instead of retrying (ADR 0011, ADR 0028).
- A fleet supervisor starts, stops, and reports bot actors.

## Process lifecycle

Botflota runs in the foreground and owns no service management. It shuts down
in order on SIGINT or SIGTERM, draining each bot's outbox before disconnecting
(ADR 0011). Starting, stopping, and restarting the process belong to systemd,
Docker, or another supervisor. Botflota neither detects nor invokes one. The
CLI has no `stop` or `restart`; `fleet status` reads the API, and config hot
reload is automatic (ADR 0009, ADR 0025).

## Logging

One structured logger (`log/slog`) writes plain, column-aligned lines with no
ANSI escapes, so `journalctl` and `docker logs` show them verbatim:
`[time] LEVEL [pkg/file.go:line] message key=value`. `log_level` sets the level
for the application and `log_level_deps` for logs forwarded from dependencies
such as the channel and the device store; `off` silences a category. Levels follow one rule: debug
for
per-message detail, info for lifecycle and state, warn for recoverable
conditions, and error for failures. Message bodies are debug-only, and secrets
stay masked by type (ADR 0014). A System One decision logs its profile, question,
answer, and confidence; the LLM logs realization and tool calls.

## Config layout

A config file is global settings, an optional `shared` catalog, and `scopes`
(ADR 0016, ADR 0031). Each scope holds its own system_one, llms, actions, graphs,
and bots; a bot's `system_one` and `llm` resolve in the scope first and then in
`shared`. `include` pulls other files that use the same shape; several files
may extend one scope, paths resolve relative to the including file, and a file
loaded twice is skipped, so diamond includes and cycles are safe. Global
settings and `shared` are only valid in the main file, and scope is never
derived from folders (ADR 0016). A relative `data_dir` resolves against the
directory of the config file, like include paths; an empty one falls back to
the XDG data home.

## Configuration pipeline

```
YAML -> strict decode -> secret references -> semantic validation -> engine
```

`${ENV_VAR}` resolves only in the fields enabled for secrets, after decoding,
so secret characters cannot break the YAML; any other string rejects it.

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
