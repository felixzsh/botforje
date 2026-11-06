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
(`SetupRunner`, `DevicePresence`, the store ports), and the two that back
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
  YAML, SQL, or a channel or LLM SDK.
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
  reports about the past (ADR 0035, ADR 0037). `adapters/testchannel` is the
  in-memory channel that proves onboarding stays adapter-local, and
  `adapters/conformance` is the reusable suite an adapter runs against its own
  definition. `adapters/installed` lists the shipped adapters, so `cmd/botflota`
  iterates the registry and names none of them (ADR 0052, ADR 0054).
- `instance` — the exclusive lock that makes one process the owner of an
  instance's data directory (ADR 0039).
- `egress` — the outbound HTTP client an instance runs with: the owner's policy
  is enforced on the connection itself, so a request step, media fetched by
  `url`, and a redirect pass the same check (ADR 0048).
- `cmd/botflota` — argument parsing and assembly.
- `internal/` — only a shared implementation detail that is not a capability
  and has no owner; nothing lives there today.

Dependencies point along `config → spec` and `engine → spec`; neither `config`
nor `engine` imports the other, and both use the core template language
(ADR 0034). `config` also reads the `egress` mode it validates (ADR 0048).

`webhook` delivers the instance's outbound events as signed HTTP POSTs over a
durable queue (ADR 0045). Planned capabilities land as `dashboard` (embedded
UI), `auth` (authority and credentials), and one package per channel or model
provider under `adapters/`. The dashboard edits behavior by publishing
config through the API; it never becomes a second source of truth (ADR 0002).

The dashboard is optional operator tooling, not the interface a host must use.
A host owns its application and can retain integration credentials server-side;
its backend calls the same API as other clients. Visual authoring and assisted
onboarding are outside this runtime's commitments (ADR 0046;
`integration.md`). Scope authorization is logical isolation, not a sandbox for
untrusted definitions; the owner can bound local file reads with a scope file
root (ADR 0047), outbound HTTP with an egress policy (ADR 0048), and a scope's
secret names with a secret catalog (ADR 0049). Shared CPU, memory, and fairness
stay the host's (`integration.md`).

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
- Peer — the durable identity of one person a scope knows: their provider
  identity (provider and an opaque subject), a best-effort display name, and
  first/last live inbound. One per (scope, provider, subject), shared by the
  scope's bots. There is no universal phone field or getter; a subject that
  contains phone digits is still opaque (ADR 0029, ADR 0051).
- Conversation target — the conversation a message belongs to: the provider, a
  kind (direct or group), an opaque thread id, and an optional topic. A sender
  and a thread are different values, so one person in a direct chat and in a
  group has both (ADR 0051).
- Directory entry — an entry of one bot's binding, keyed by owner,
  peer identity, and source identity, and copied only when the bot sets
  `import_contacts`. It is never a chat; importing it also registers the peer
  as a scope identity, so the directory holds the people the account knows.
  The derived `address_book_imported` means an import supplied the identity,
  not that the account still has it saved (ADR 0037, ADR 0051).
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
the runtime context. A local `path` is read inside the scope's file root when
the owner set one (ADR 0047); without a root, the process's own file
permissions apply.

A stored message keeps its kind and visible text as derived columns and its
structured payload as `content` JSON, so a poll's options or a pin's
coordinates survive the store and the read API can serve them.

## Message lifecycle

Inbound:

1. Channel receives an event, classifies it into a kind, maps it to an
   `IncomingMessage` carrying its own sender identity and conversation target,
   and resolves what the wire needs; a message with no known sender or
   conversation, or an unclassifiable live payload, is dropped (ADR 0020,
   ADR 0036, ADR 0051).
2. The fleet routes the event to the bot; the bot's filter drops it (shared
   threads, allow/ignore lists) or passes it to the engine, which records the
   inbound on the peer identity before routing (ADR 0029). The write that
   claims `first_seen` is the first-contact hook: the engine hands it to the
   configured notifier, whose webhook sink queues signed deliveries
   (ADR 0045). The dedup check in step 5 reads that conversation first, so
   only a message about to run writes it.
3. Graph engine loads the session, or creates one at the graph root. On
   creation the incoming message is evaluated before the root's entry
   actions: if it transitions, the root message is skipped; if not, the
   root's `on_enter` runs.
4. Deterministic matching runs over candidate nodes: visited nodes (oldest
   first) plus the current node last (ADR 0010). Edges with triggers are
   fuzzy matched; the best score wins and ties go to the later candidate.
   The default edge is only taken from the current node.
5. A redelivery whose provider id the destination already stores stops here,
   before any action runs and before the peer or chat is written (ADR 0040).
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
    (ADR 0040). The peer identity and chat created in step 2 sit outside
    that transaction, so a failed turn can leave those two rows and nothing
    else. The outbox worker then delivers the replies with the pacing below.

A conversation may be handed to a human instead (ADR 0043). A live message
that leaves the binding without Botflota sending it — an operator reply
from another client, told apart from the bot's own echo by the channel — starts
a pause for that conversation; the API may also hold one until it is released.
While it is held, the fleet stores each inbound message for history and
deduplication but runs no transition and queues no reply, and the session does
not change. Taking the hold fails the conversation's still-pending replies
first, and the outbox worker rechecks a reply just before sending it, so
nothing automatic overtakes the human. The pause length is the bot's
`settings.external_reply_pause`; the state is in memory, so a restart returns
every conversation to automatic.

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
`{{peer.id}}`, `{{peer.provider}}`, `{{peer.subject}}` and `{{peer.name}}`,
`{{message.*}}`,
`{{bot.*}}`, tool arguments as `{{input.<name>}}`, and request results as
`{{node.<request-name>.resp.body.*}}`,
`{{node.<request-name>.resp.headers.*}}`, `{{node.<request-name>.status}}`,
and `{{node.<request-name>.duration}}`. Tool result syntax is still open
(Phase 4).

`peer.id` is the peer's opaque handle within the scope, stable for the life of
the identity; `peer.provider` and `peer.subject` are its comparable key, and
`peer.name` is best-effort presentation. The old `contact.*` root is retired
with no alias (ADR 0018, ADR 0020).

`graph` and `node` are read-write; `peer`, `message`, `bot`, and `input` are
read-only, and `node` resets on every node entry. A missing path, an unknown
root, and a whole map or slice are errors, never empty or literal text; a
literal `{{` is written `\{{` (ADR 0018). The language lives in the core
(`template.go`), the runtime context in `engine` (`context.go`), and the
reference checks in `config` (`config/template_validation.go`) (ADR 0034).

The language resolves only runtime conversation values. `${NAME}` is a separate
secret mechanism, allowed only in model keys and request header values, and it
never combines with `{{...}}` (ADR 0014). A scope the owner gave a secret
catalog resolves `${NAME}` from that file alone (ADR 0049), and an outbound call
obeys the instance egress policy (ADR 0048).

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

## Webhooks

The instance can mirror outbound events to an external system through signed
webhooks (ADR 0045). The engine reports a peer's first inbound through
the core `PeerFirstSeenNotifier` port; the `webhook` package implements it as a
sink that freezes one envelope per event and queues one delivery per
subscribed target in SQLite. A worker polls the queue, signs the raw body with
HMAC-SHA256, POSTs it, retries network failures, `429`, and `5xx` with
exponential backoff, and records the outcome. The queue never stores a secret:
the signing secret is resolved from the running configuration at delivery
time, so a target the configuration no longer holds fails its pending
deliveries. The list is an instance setting in the main file and needs a
restart.

## Chats and sessions

A chat is the durable conversation, keyed by (scope, bot, provider, kind,
thread, topic). Its known participant is an optional peer reference that only
explicit evidence fills; it is never derived from the thread, and a shared
conversation is not one person's identity (ADR 0051). It never ends and gathers
every message. A
session is one bounded run of the graph for a chat: it starts at the root and
expires after the graph timeout (or `default_timeout`). An expired session is
replaced by a new run; the next message starts at root. Chat, session, and
message history live in SQLite (ADR 0026).

Every stored message carries its provenance (ADR 0026): `ingest_kind` says
whether it came from live traffic, a history sync, or the API; `routing` says
which matching layer or decision handled the inbound message; and `origin`
says whether an outbound body is `preset`, `llm`, `operator`, or `api` (an
API-origin send, ADR 0044). An inbound import carries no origin, and an
imported outbound message reports `sync` with `unknown` origin, because the
past cannot say who wrote an outbound message outside Botflota (ADR 0026).

The account-wide history sync WhatsApp delivers at link time is imported
inertly (ADR 0035): `bot bind` and `fleet run` share one importer, every 1:1
content message is kept under its kind (ADR 0036), and every row is `sync`
with `unknown` routing and no session run; an inbound row carries no origin,
and an imported outbound body is `unknown` origin. The import never runs the
engine and ignores the bot filter, because it is not a conversation turn;
`channel.options.history_sync: false` turns it off. A conversation registers its
peer and chat only when it stored at least one message or call, so the
control-only shells WhatsApp sends for the account's directory never become
chats. The directory itself is a separate, opt-in import
(`channel.options.import_contacts`): it fills the `directory_entries` resource per bot
and never touches the scope's peers, chats, or the engine (ADR 0037).
`provider_message_id` (the channel's own id) is the dedup key, scoped by the
bot that owns the message and by the destination it belongs to, both for a
redelivered sync chunk and for a live reply the sync echoes afterwards. The
`PUSH_NAME` chunk names the peers the import knows, and importing a
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
`settings.history_import_max_bytes` for what its binding may import.
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
scope routes are `/api/v1/scopes/{scope}/` plus `bots`, `config`, `peers`, and
`messages`; a bot adds `chats`, `graph-runs`, `channel`, `directory`, `binding`,
and `history` under `/bots/{bot}`. Every addressable object carries
its scope, a
message carries its bot as a (scope, bot) pair, and a chat is addressed by that
pair plus `?chat_id=<id>`; there is no legacy `contact=<subject>` selector or
phone projection beside the neutral references (ADR 0054, PLAN.md "no backward
compatibility"). `messages` returns
the last messages of a
chat, or the ones newer than `?after_id=`, which is how `message tail --follow`
polls, and each message carries its kind, its visible text, and the structured
`content`; `chats` lists a bot's chats with their peer identities, which is how
`chat list` reads them; `status` and `bots` report each bot's active sessions;
`POST chats/sync` asks the account for older messages of one chat over the live
connection and reports the batch counts. A scope's `messages` route also
accepts a `POST`: an API-origin send that stores and queues one text message
to any peer — creating the peer and the chat when missing — so the bot's
outbox delivers it like a reply (ADR 0044). Errors use one shape,
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
and the log gets the reason — and the setup session's display payload reaches
only the response that asks for it, never a log (ADR 0025).

## Binding and setup

Binding a bot to its channel account is a separate step from running it:
`bot bind` drives the neutral setup lifecycle (ADR 0053). It resolves the
adapter's advertised method, begins a session through the API of a running
instance, and answers the session's typed steps — display, input, approval, and
profile — without a provider-specific form. For WhatsApp the `qr` method
renders the QR in the terminal, and the resulting companion credentials live in
the adapter-owned device store in the shared SQLite file (ADR 0006). The
companion registers under the bot's `channel.options.device_name` (default
`Botflota`), the name the account shows in Linked devices; it is sent only when
registering, so an existing link keeps its name.
Two key systems are involved,
and only one is software: the companion identity (Noise and Signal keys, prekeys,
registration id) lives in the device store and can run on a server, while a
passkey lives in an authenticator on the account owner's device and never leaves
it (ADR 0028).

When the server requires a passkey, the account performs the ceremony and the
companion receives the handoff; if the server asks the companion instead, the
pending step is reported and the bot stays stopped (ADR 0028).

Right after linking, WhatsApp pushes the account-wide history sync. The same
inert importer captures it in `bot bind` and in `fleet run`, whichever
connection sees a chunk (ADR 0035). Pairing registers as a Desktop companion
and always imports what arrives while its connection is alive. With the
`full_history=true` setup parameter (`bot bind --param full_history=true`) it
also registers with `RequireFullSync=true` and a wide window, and waits until
that sync completes, the chunks go idle for a tolerant gap, or a long wait
budget of its own runs out; without it, only the default
recent window arrives and `chat sync` pages older messages per chat. The account
remains the authority over the actual window.

Binding operations travel over the API of a running instance (ADR 0039): `GET`
and `DELETE` on `.../bots/{bot}/binding` inspect and detach a committed
binding, `GET .../binding/methods` lists the advertised methods, `POST
.../binding/setups` begins a setup session, `GET .../binding/setups/{id}` reads
it, `POST .../binding/setups/{id}/inputs` answers one step, and `DELETE
.../binding/setups/{id}` cancels the session.
Detaching ends a live session and waits for its goroutine, stops the bot so its
channel disconnects, removes the adapter's credentials, and fails the replies
its queue still owes — one operation under the bot's lock, so the writes of one
bot never overlap. The CLI drives those routes while another process owns the
instance and works locally when it does not; replacing a binding is
`bot bind --force`, whichever way the instance is running.

An account change never changes history. Unbinding keeps the bot's chats,
messages, sessions, and directory, so a rebind or a replacement picks them
up; `adapters/whatsapp/pair.go` removes credentials and the account record only.
Deleting a bot's history is a separate explicit
operation, and a migration moves chats, messages, and the directory to
another bot of the same scope — a recreated bot or a peer bot — while peer
identities stay with the scope they are shared by (ADR 0029). A migration is a move, not a copy:
it runs with both bots stopped, fails the source's queued replies before the
old link can send them, and ends the source's active sessions instead of
pretending the destination's graph sits at the same node. Stored message
ownership follows its chat in the same transaction, including the owner columns
used for deduplication; its target, provider ID, and historical metadata stay
unchanged. Current data ownership is not binding/message-space provenance
(ADR 0051); that remaining policy stays in P5. Neither operation runs as a
side effect of binding, unbinding, or a config edit. Both are
planned in `docs/next-plan.md` and settled in ADR 0042.

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
  backpressure: while the adapter runs, a full inbound queue blocks the
  channel's event handler instead of dropping the user's message (ADR 0040);
  stopping the adapter releases a blocked handoff, dropping that message
  rather than blocking shutdown. A shared source follows
  the same rule: its adapter-owned reader keeps every logical view's buffer
  bounded, so a full buffer couples the views' progress to that one source
  rather than promising each view independent throughput. A wait on the
  source is cancelable, and releasing one logical view frees that view's
  waits without closing a peer's view. Shared transport health is never read
  as a per-binding authorization revocation (ADR 0040, ADR 0052).
- The acknowledgement and replay boundary is explicit, not inferred. A source
  that can confirm gets the agreed processing outcome only after a committed
  turn, an already-recorded duplicate, or a deliberate discard; handoff
  confirms nothing, and a failed processing stays replayable (ADR 0004,
  ADR 0040). Delivery is at-least-once from the durable outbox — a committed
  turn stores its replies and the worker retries them across a restart — and
  a send success means the channel accepted the message, never recipient
  delivery (ADR 0040, ADR 0052). No durable inbox is added on top of that
  queue, and a source that cannot replay is not promised losslessness.
- Grouping, fairness, capacity, and any further durable buffering for a
  shared source remain deployment decisions, not runtime guarantees
  (ADR 0046, ADR 0054; `channels/open-decisions.md`).
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
