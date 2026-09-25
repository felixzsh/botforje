# Architecture

Component boundaries, ports, and the lifecycle of a message. This document
describes intent; update it as the code exists.

## Ports and adapters

Three independent ports sit beside the config engine and persistence:

- `MessageChannel` — transport port. A channel is one implementation that
  reaches a messaging provider; the provider (WhatsApp) can have several
  channels, such as whatsmeow today or the official Cloud API later.
- `SystemOneProvider` — **Phase 8, not implemented yet.** System One port for
  routing, policy, tool selection, and value mapping: typed answers (a choice
  from a closed set, a score, or a probability) with confidence and no
  generated text. Jev (TypeSafe) is the first planned adapter (ADR 0030).
- `LLMProvider` — model port for reply realization and the tool loop. It
  exists in an interim shape that still classifies transitions, and the fleet
  injects no implementation yet; Phase 8 replaces `Classify` with the
  System One port and keeps generation for text only (ADR 0004, ADR 0030).

The other core ports follow the same rule: what exists is small and wired
(`PairingRunner`, `DevicePresence`, the store ports), and the two that back
the atomic turn and delivery — `TurnStore` and `OutboxStore` — are why the
engine writes no rows directly (ADR 0040).

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
separate: `adapters/sqlite` opens the database and `adapters/sqlite/stores`
implements the store ports; `adapters/whatsapp/channel` implements
`MessageChannel`.

Behavior types (`Graph`, `Action`, `Node`) live in `spec`; a store may build
them directly; the config loader stays their only builder, and the dashboard
publishes behavior as config (ROADMAP Phase 15, ADR 0002).

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
  adapter may import the core, never the reverse. `adapters/sqlite` opens the
  database and holds the DDL; `adapters/sqlite/stores` implements the store
  ports, one file per repository. `adapters/whatsapp` pairs a companion and
  owns its device store; `adapters/whatsapp/channel` holds the live connection
  and the `Adapter`, and `adapters/whatsapp/ingest` stores what WhatsApp
  reports about the past (ADR 0035, ADR 0037).
- `instance` — the exclusive lock that makes one process the owner of an
  instance's data directory (ADR 0039).
- `cmd/botflota` — argument parsing and assembly.
- `internal/` — only a shared implementation detail that is not a capability
  and has no owner; nothing lives there today.

Dependencies point along `config → spec` and `engine → spec`; neither `config`
nor `engine` imports the other, and both use the core template language
(ADR 0034).

Planned capabilities land as `dashboard` (embedded UI), `auth` (authority and
credentials), `webhook` (outbound delivery), and one package per channel or
model provider under `adapters/`. The dashboard edits behavior by publishing
config through the API; it never becomes a second source of truth (ADR 0002).

`fleet run` is the composition root: it loads config, opens stores and channel
devices, builds the fleet, and gives those live dependencies to `api`. API
handlers start with the smallest direct operation; a narrow service is
extracted only when a second real caller needs the same logic. There is no
generic `control` facade.

## Control plane

One process owns an instance: `fleet run` holds the exclusive lock on
`data_dir`, and a local command that writes instance state must take the same
lock first (ADR 0039). Everything that inspects or controls a running instance
travels through the REST API it serves:

    CLI (operational)  --+
    dashboard           --+--> REST API --> fleet + store ports --> SQLite
    host integration    --+

    CLI (offline) ----------> config, stores, device store   (no owner running)

SQLite is state, not an event bus: a read sees the latest committed change and
no consumer is notified. The API never reads the database on the CLI's behalf,
and the CLI never falls back to the database while the instance is owned. The
CLI is a local client that may read the config file to find the API; the
dashboard and integrations are API-only and never see local files.

## Domain model

- Bot — identity (account) plus settings, the reference to one graph, and
  optional references to one system_one and one llm (ADR 0031).
- Config identity — the YAML map key identifies a scope or entity and is used
  by references and persisted state. Optional `display_name` is presentation
  metadata only; interfaces fall back to the key when it is absent. Changing
  a key is an identity change, not a display rename (ADR 0016).
- Sender — the durable profile of one person who writes to bots in a scope:
  phone, push name, and first/last seen, keyed by (scope, phone). The
  bot-person thread is the chat; a person may write to several bots of the same
  scope (ADR 0029).
- Contact — an address-book entry of one bot's linked account, keyed by
  (scope, bot, phone) and copied only when the bot sets `import_contacts`. It
  is an identity the account knows, never a chat or a sender, and it takes no
  part in routing (ADR 0037).
- Graph — state machine with root, fallback, timeout, a routing override
  (ADR 0030), and a map of nodes.
- Node — goal, policy, entry/exit actions, tools, and optional node-local
  context.
- Edge — transition with a required user intent, except the default
  (catch-all) edge. `triggers` is an optional deterministic fast path;
  System One resolves input that matches no trigger (ADR 0030).
- Action — ordered steps (`message`, `request`, `location`, `media`, `contact`,
  `poll`) with guards and declared `inputs`; exposed to the LLM when a node
  lists it in `tools`.
- Tool — an action the LLM may invoke with validated arguments; an input
  may bind to a node or graph variable (read and write).
- Node context — node variables that reset when the node is entered again.
- Visited nodes — ordered list of nodes visited (current last) used for
  routing and as LLM context; reordered on each visit, never reset by
  navigation.
- Graph context — session-wide variables that persist across nodes.
- Scope — the top-level config namespace holding its own system_one, llms, actions,
  graphs, and bots (ADR 0016, ADR 0031). A scope may hold a product
  environment, a team, or a workload; the name is free. References and sessions
  never cross scopes.
- Shared catalog — the instance-level `system_one` and `llms` a host provides, in
  the main config file only; a bot resolves a name in its scope first, then in
  `shared`. It is not a scope (ADR 0031).
- System One model (SOM) — a named model that answers typed decisions with confidence for
  routing and policy, referenced by a bot with `system_one`; it resolves in the scope
  or in `shared` (ADR 0030, ADR 0031).
- LLM — a named model profile (provider, model, credentials, realization
  defaults, bounds) referenced by a bot with `llm`; it resolves in the scope
  or in `shared` (ADR 0031).

## Message kinds

Every message carries a channel-neutral kind: `text`, `image`, `video`,
`audio`, `document`, `sticker`, `location`, `contact`, `poll`, `event`,
`interactive`, `commerce`, `call`, or `unknown` (ADR 0036). The kind names the
payload, and the payload is typed content: a sealed `MessageContent` with one
concrete type per kind, `VisibleText()` for the text a reader shows and the
engine routes on, and `SendableContent` for the subset an action may emit.
`IncomingMessage` and `OutgoingMessage` carry `Content`, so a kind and its
payload cannot disagree. The attachment bytes never enter the runtime context.

A channel implements the subset it can produce and send: the import keeps every
recognized kind as an anchor, live traffic delivers every recognized kind to the
engine, and an outbound step may emit the sendable ones. A channel that cannot
deliver a content type answers `ErrUnsupportedKind` instead of dropping the
message.

An action sends `message` (text), `location`, `media` (image, video, audio,
document, sticker from a templated `url` or a local `path`), `contact`, or
`poll`. The channel reads media bytes, so a large file or a secret stays out of
the runtime context.

A stored message keeps its kind and visible text as derived columns and its
structured payload as `content` JSON, so a poll's options or a pin's
coordinates survive the store and the read API can serve them.

## Message lifecycle

Inbound:

1. Channel receives an event, classifies it into a kind, maps it to an
   `IncomingMessage`, and resolves the sender to a phone number; a message with
   no known phone number, or an unclassifiable live payload, is dropped
   (ADR 0020, ADR 0036).
2. The fleet routes the event to the bot; the bot's filter drops it (groups,
   allow/ignore lists) or passes it to the engine, which upserts the sender
   profile before routing (ADR 0029). The dedup check in step 5 reads that
   sender and chat first, so only a message about to run writes them.
3. Graph engine loads the session, or creates one at the graph root. On
   creation the incoming message is evaluated before the root's entry
   actions: if it transitions, the root message is skipped; if not, the
   root's `on_enter` runs.
4. Deterministic matching runs over candidate nodes: visited nodes (oldest
   first) plus the current node last (ADR 0010). Edges with triggers are
   fuzzy matched; the best score wins and ties go to the later candidate.
   The default edge is only taken from the current node.
5. A redelivery whose provider id the chat already stores stops here, before
   any action runs and before the sender or chat is written (ADR 0040).
   Steps 6–8 are the Phase 8 target: the fleet injects no model yet, so only
   the deterministic path runs today.
6. When the graph routing mode is `always`, or nothing matched and the mode
   is `fallback`, System One decides: one Choice question over the candidate
   edge intents, checked against the graph's confidence threshold. Below it
   the graph takes the default edge or escalates; the LLM never classifies
   (ADR 0030).
7. A node with tools handles the message through the pipeline (ADR 0033):
   System One decides, the LLM reads free text, and code applies. The node
   stays unless an exit edge is taken. The LLM loop (ADR 0033) is the opt-in
   alternative for calls a pipeline cannot bound.
8. Tool results feed the next step; a missing required argument is a tool
   error that asks the user, storing the answer in the bound variable.
   Neither model invents a transition; edges are the only exits.
9. Engine runs the selected transition: exit actions, target node, entry
   actions. Entry actions produce the prescribed reply, so a bot with no `llm`
   needs no provider (ADR 0032).
10. Effective policy composes node policy (always) and message policy
    (optional refinement). Reply text is prescribed, LLM-realized, or both
    (anchor plus fallback); a policy check runs before anything is queued.
11. The session, the stored messages, the chat activity, the guards, and the
    queued replies commit as one transaction; a failure rolls all of it back
    (ADR 0040). The sender profile and chat created in step 2 sit outside
    that transaction, so a failed turn can leave those two rows and nothing
    else. The outbox worker then delivers the replies with the pacing below.

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

The engine implements all three, but no LLM provider adapter ships yet and
the fleet injects none, so a running bot answers prescribed today; the other
two arrive with Phase 8.

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

Every stored message carries its provenance (ADR 0026): `ingest_kind` says
whether it came from live traffic, a history sync, or the API; `routing` says
which matching layer or decision handled the inbound message; and `origin`
says whether an outbound body is `preset`, `llm`, or `operator`. An imported
message reports `sync` with `unknown` origin, because the past cannot say who
wrote an outbound message outside Botflota.

The account-wide history sync WhatsApp delivers at link time is imported
inertly (ADR 0035): `bot pair` and `fleet run` share one importer, every 1:1
content message is kept under its kind (ADR 0036), and every row is `sync`
with `unknown` origin and routing and no session run. The import never runs the
engine and ignores the bot filter, because it is not a conversation turn;
`settings.history_sync: false` turns it off. A conversation registers its
sender and chat only when it stored at least one message or call, so the
control-only shells WhatsApp sends for the account's address book never become
chats. The address book itself is a separate, opt-in import
(`settings.import_contacts`): it fills the `contacts` resource per bot and
never touches senders, chats, or the engine (ADR 0037).
`provider_message_id` (the channel's own id) is the dedup key, both for a
redelivered sync chunk and for a live reply the sync echoes afterwards. The
`PUSH_NAME` chunk names the senders the import knows, and importing a
conversation widens the chat's activity to the imported range. `chat sync`
pages older messages back from the chat's oldest stored message in sequential
batches, re-anchoring on the oldest message each page returned, until a page
carries nothing older or the phone reports no access. It runs over the live
fleet connection, so one phone number keeps one companion connection: the
channel exposes `botflota.HistorySyncer`, the API serves
`POST /api/v1/scopes/{scope}/chats/sync`, and the CLI calls that endpoint.

The session also keeps the visited nodes (ADR 0010): the ordered list of
nodes used for routing and handed to the LLM as context, bounded to a
configured maximum.

Stored history is bounded per limit set (planned, Phase 7;
`docs/access-policy.md`): `max_stored_message_bytes` sums the stored `body` and
`content` bytes of the set's scopes — inbound, outbound, and imported alike —
not the size of the database file, and each bot carries its own
`settings.history_import_max_bytes` for what its linked account may import.
When a set has no room left, import stops first and reports a partial import,
then the bots of that set pause their turns: a bot never answers a message it
could not store, so the atomic turn of ADR 0040 still holds. The pause
protects consistency, not delivery — WhatsApp decides whether it retransmits
what arrived while the bot was paused — so the reported state tells an
operator that messages may have been missed. The condition is part of the
bot's reported status, not only a log line. Beyond the MVP this may become
retention — dropping the oldest messages to make room — or answering while
storing nothing.

## REST API

The API's scope-coverage and authority contract, including current status and
the target, lives in [`api.md`](api.md); this section describes the current
composition and mechanics.

`fleet run` hosts the API in the same process when `port` is set; `port: 0`
keeps it off and the address defaults to `127.0.0.1`. The read routes live
under `/api/v1`. Instance routes are `status`, `bots`, `config`, and `tokens`;
scope routes are `/api/v1/scopes/{scope}/` plus `bots`, `config`, `chats`,
`contacts`, `sessions`, and `messages`. Every addressable object carries its scope, a
message carries its bot as a (scope, bot) pair, and a chat is addressed by that
pair plus the sender's phone number. `messages` returns the last messages of a
chat, or the ones newer than `?after_id=`, which is how `message tail --follow`
polls, and each message carries its kind, its visible text, and the structured
`content`; `chats` lists a bot's chats with their sender profiles, which is how
`chat list` reads them; `status` and `bots` report each bot's active sessions;
`POST chats/sync` asks the phone for older messages of one chat over the live
connection and reports the batch counts. Errors use one shape,
`{"error":{"code","message"}}`.

One middleware resolves the credential to a `Principal` through a small
`PrincipalResolver` port (`Auth` is the built-in implementation), and handlers
authorize against it (ROADMAP Phase 10, Phase 13). Exactly two authorities
exist, with no roles: the instance owner (the owner token) sees every scope,
instance settings, and credential management; a scoped access token reads and
changes the resources of its own scopes and nothing else. A scoped principal
requesting another scope receives 404, never 403, so it cannot infer that the
scope exists (ROADMAP Phase 13).

The owner token is created on first run, stored as a hash in SQLite, and
written once to `owner.token` under `data_dir` with mode 0600 for the CLI;
`fleet run --new-owner-token` regenerates it. Scoped access tokens are minted
by the owner with `POST /api/v1/tokens`, stored hashed with optional expiry,
listed without hashes, and revoked with `DELETE /api/v1/tokens/{id}`. A mint
grants the scopes the owner names: several tokens may share a scope unless
the instance sets `exclusive_scopes`, which makes the mint refuse a scope
another active token already holds and the server refuse to start on an
overlap already stored (restart to apply; `docs/api.md`). Every request
carries `Authorization: Bearer`. Browser sessions and CSRF arrive with
the dashboard (Phase 11); the resolver is the seam they replace. `config`
serves the typed model with secrets masked (ADR 0014). Handlers stay small and
direct; a narrow service is extracted only when a second real caller shares
the operation.

Requests are budgeted in fixed one-minute windows: one allowance per
credential, keyed from the credential's own hash, and a separate allowance
per source address for attempts that carry no usable credential, so guessing
is bounded without one source spending another's budget. A refusal is the
standard error shape with code `rate_limited` and a `Retry-After` header.
The budgets are instance settings (`rate_limit`): the defaults are 600 per
credential and 60 failed attempts per minute, and `0` turns one off.
Public errors never carry upstream detail — the caller gets a stable code
and the log gets the reason — and the pairing session's `code` reaches only
the response that asks for it, never a log (ADR 0025).

## Pairing

Linking a bot to WhatsApp is a separate step from running it: `bot pair` opens a
channel, shows the QR code or the pairing code, and stores the resulting
credentials in the shared SQLite file (ADR 0006). The companion registers under
`device_name` (default `Botflota`), the name the phone shows in Linked devices;
it is sent only when registering, so an existing link keeps its name.
Two key systems are involved,
and only one is software: the companion identity (Noise and Signal keys, prekeys,
registration id) lives in the device store and can run on a server, while a
passkey lives in an authenticator on the account owner's device and never leaves
it (ADR 0028).

When the server requires a passkey, the phone performs the ceremony and the
companion receives the handoff; if the server asks the companion instead, the
pending step is reported and the bot stays stopped (ADR 0028).

Right after linking, WhatsApp pushes the account-wide history sync. The same
inert importer captures it in `bot pair` and in `fleet run`, whichever
connection sees a chunk (ADR 0035). Pairing registers as a Desktop companion
and always imports what arrives while its connection is alive. With
`--import-history` it also registers with `RequireFullSync=true` and a wide
window, and waits until that sync completes, the chunks go idle for a tolerant
gap, or a long wait budget of its own runs out; without it, only the default
recent window arrives and `chat sync` pages older messages per chat. The phone
remains the authority over the actual window.

Pairing operations travel over the API of a running instance (ADR 0039):
`POST`, `GET`, and `DELETE` on `.../bots/{bot}/pairing` start, read, and
cancel a session, and `DELETE .../bots/{bot}/account` ends a live session
and waits for its goroutine, stops the bot so its channel disconnects,
removes the credentials, and fails the replies its queue still owes — one
operation under the bot's lock, so the writes of one bot never overlap. The
CLI drives those routes while another process owns the instance and works
locally when it does not; replacing a pairing is an unpair followed by a
pair, whichever way the instance is running.

An account change never changes history. Unlinking keeps the bot's chats,
messages, sessions, and contacts, so a relink or a replacement picks them up;
`adapters/whatsapp/pair.go` removes credentials, the account record, and the
pairing status only. Deleting a bot's history is a separate explicit
operation, and a migration moves chats, messages, and contacts to another bot
of the same scope — a recreated bot or a peer bot — while senders stay with
the scope they are shared by (ADR 0029). A migration is a move, not a copy:
it runs with both bots stopped, fails the source's queued replies before the
old link can send them, and ends the source's active sessions instead of
pretending the destination's graph sits at the same node. Neither operation
runs as a side effect of pairing, unpairing, or a config edit. Both are
planned in `docs/current-plan.md` and settled in ADR 0042.

## Concurrency

- One actor per bot: inbound events and sessions for a bot are serialized;
  different bots run concurrently.
- Pairing start, cancel, and unpair of one bot also run one at a time, under
  the API's per-bot lock (see the pairing operations above).
- The engine and the outbox are independent, each serialized; a bot keeps
  reading inbound while its outbox paces earlier replies (ADR 0011). The
  outbox is durable (ADR 0040): replies wait in SQLite, survive a restart,
  and are retried with bounded backoff before they are given up on; the
  oldest pending reply heads the queue and a scheduled retry blocks the
  ones behind it, and unlinking a bot fails what its link still owes.
- Config reload validates a whole YAML candidate, then advances only safe
  changed scope snapshots; unchanged scopes keep their actors and channels.
  Saved YAML and active runtime may differ while graph sessions or restart-
  required settings are pending. Include changes use the same pipeline
  (ADR 0009).
- One WhatsApp phone number belongs to one Botflota bot. Multiple linked
  companion devices for the same number would receive the same traffic and can
  produce duplicate replies.
- The tool loop runs inside the bot actor and is bounded (max tools per
  message, timeout); a slow provider cannot stall other bots.
- Bounded queues between the channel, the engine, and the outbox provide
  backpressure: a full inbound queue blocks the channel's event handler
  instead of dropping the user's message (ADR 0040).
- No shared mutable state between bots; shared services are read-only or
  synchronized.
- Reconnect uses capped exponential backoff with jitter; a bot that is logged
  out, banned, or rejected with 428 (passkey required) or 463 (reachout
  timelock) stops and reports instead of retrying (ADR 0011, ADR 0028).
- A fleet supervisor starts, stops, and reports bot actors.

## Process lifecycle

Botflota runs in the foreground and owns no service management. It shuts down
in order on SIGINT or SIGTERM, draining each bot's outbox before disconnecting
(ADR 0011). Unpair finishes a send already in progress and fails the remaining
queued replies before removing the account (ADR 0040). Starting, stopping, and
restarting the process belong to systemd, Docker, or another supervisor.
Botflota neither detects nor invokes one. The CLI has no `stop` or `restart`;
`fleet status` reads the API, and config hot reload is automatic (ADR 0009,
ADR 0025).

## Logging

One structured logger (`log/slog`) writes plain, column-aligned lines with no
ANSI escapes, so `journalctl` and `docker logs` show them verbatim:
`[time] LEVEL [pkg/file.go:line] message key=value`. The daemon writes it to
stderr and owns nothing else: the supervisor captures, rotates, and ships it.
Botflota keeps no log file and stores no logs in the database (ADR 0038).

A one-shot command answers with a result on stdout and diagnostics on stderr,
and stays at `warn` unless `--verbose` raises it, because `log_level` and
`log_level_deps` describe the service rather than a command a human runs once.
`log_level` sets the level for the application and `log_level_deps` for logs
forwarded from dependencies such as the channel and the device store; `off`
silences a category. Levels follow one rule: debug for per-message detail, info
for lifecycle and state, warn for recoverable conditions, and error for
failures. Message bodies are debug-only, and secrets stay masked by type
(ADR 0014). A System One decision logs its SOM, question, answer, and
confidence; the LLM logs realization and tool calls.

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

Scope and entity map keys are stable identifiers. Optional non-empty
`display_name` labels are used by interfaces and fall back to those keys. They
are supported on scopes, bots, graphs, nodes, actions, and LLM profiles;
future SOMs follow the same rule. A key change does not imply
a rename and can orphan references or runtime state; only a future explicit
operation may coordinate an identity migration. Scope provisioning generates
a unique key when the API creates a scope; manually authored scopes keep their
chosen key.

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
