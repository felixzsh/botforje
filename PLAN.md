# Botflota — Plan

Phase-by-phase plan to the MVP. The MVP scope is defined in `docs/scope.md`;
settled decisions live in `docs/adr/`; open decisions are listed at the end.

## How to read this plan

- One phase at a time, in order. Do not start a phase before its
  dependencies are done.
- Every phase ends with a gate: tests that prove the phase's behavior. A
  phase is not done until its gate passes.
- The plan is not a contract. Adjust it when real needs appear, and record
  course changes as ADRs.
- Deliverables are checkboxes. Check one in the same commit that completes it;
  the first unchecked box is the current work.

## Phase map

0. Foundations
1. Config engine and `config validate`
2. Templates and context
3. Persistence (SQLite)
4. Graph engine
5. WhatsApp channel (whatsmeow)
6. Fleet, filter, and outbox
7. CLI and REST API
8. System One routing, replies, and tools
9. MVP hardening and release

## Phases

### Phase 0 — Foundations

Goal: repository ready to receive code, with the riskiest dependency tested.

Deliverables:

- [x] Documentation set: `README.md`, `PLAN.md`, `docs/scope.md`,
  `docs/glossary.md`, `docs/architecture.md`, `docs/adr/`, `AGENTS.md`.
- [x] Toolchain decisions recorded: Go version and CLI approach (ADR 0012).
- [x] Time-boxed spike: pair a WhatsApp account with whatsmeow, persist the
  device store, receive and send one message. Throwaway code, not merged.
- [x] Test conventions: table-driven tests and fixture layout (AGENTS.md).

Gate: the spike proves pairing and messaging work; conventions are written.

Out of scope: any product code.

### Phase 1 — Config engine, config baseline, and `config validate`

Goal: settle the config baseline and turn YAML into a validated typed model
with precise errors. The baseline keeps evolving through the MVP and freezes
at release.

Deliverables:

- [x] Design task first: settle the config baseline (node `goal`/`policy`/`tools`,
  action `inputs`/`bind`, edge `intent`/`triggers`, message modes, scope,
  graph `routing`, `llms`). Align `config/examples/reference.yml` and the draft
  `config.schema.json` with it.
- [x] Domain types: Action, Graph, Node, Edge, Step, Bot, Settings, Guard,
  LLM. They moved to `spec`; the template language lives in the core and the
  runtime context in `engine` (ADR 0034).
- [x] Loader: main config plus `include` fragments (file, glob, directory,
  recursive), scope merging, and cycle-safe dedupe (ADR 0016).
- [x] Runtime validation in Go: strict YAML decoding plus semantic checks.
  The JSON Schema is generated from the Go types for editors only (ADR 0007).
- [x] Secret references: resolve `${ENV_VAR}` after decoding; missing variables
  fail validation (ADR 0014). The baseline resolves them in any string; the
  secret-only restriction is a Phase 7 item.
- [x] Secret values mask themselves in logs and errors (ADR 0014).
- [x] Semantic validation: reference resolution, scope isolation, uniqueness by
  (type, scope, name), node reachability, and message mode rules.
- [x] Schema generation: `invopop/jsonschema` and the regeneration step.
- [x] CLI command `config validate` with readable output and a `--json` mode.
- [x] Fixtures: valid and invalid cases for every rule.

Gate: `config validate` accepts valid fixtures and rejects every invalid fixture
with an error naming file, entity, and reason. Unit tests for merge and
semantic validation; integration tests over fixtures.

### Phase 2 — Templates and context

Goal: `{{...}}` values resolve correctly in messages and requests.

Deliverables:

- [x] Syntax and resolver for `{{path.to.value}}`.
- [x] Contexts: graph, node, contact, message, bot, and request results.
- [x] Read/write versus read-only context rules; node context resets on re-entry.
- [x] Tests: resolution, missing paths, escaping, and request result capture.

Gate: message steps render correctly from a simulated context.

### Phase 3 — Persistence (SQLite)

Goal: runtime state survives restarts.

Deliverables:

- [x] SQLite setup with the schema applied on open; pure-Go driver, WAL,
  foreign keys, busy timeout, and synchronous=NORMAL (ADR 0006).
- [x] Stores: chats, sessions, and messages.
- [x] Atomic turn: the engine commits the session, the stored messages, the
  chat activity, the guards, and the queued replies as one transaction, and
  drops a redelivery before any action runs (ADR 0040).
- [x] Repository interfaces owned by the domain; SQL stays in the adapter.
- [x] Tests: schema apply, store round-trips, and session resume after restart.
- [x] Race testing: `make test-race` (`go test -race ./...`) and use it
  whenever concurrency is involved.

Gate: a session persisted by one process resumes in a new process.

### Phase 4 — Graph engine

Goal: conversations run fully in memory, testable with no channel.

Deliverables:

- [x] `MessageChannel` port: connect, disconnect, send, and the inbound
  stream, owned by the engine; the whatsmeow adapter arrives in Phase 5
  (ADR 0004).
- [x] Session lifecycle: create at root, resume, and expire by timeout.
- [x] Visited nodes: ordered list of nodes (current last), persisted and
  never reset by navigation (ADR 0010).
- [x] Routing: candidates are visited nodes plus the current node (last); best
  trigger match wins, ties go to the most recent. Default edge only from the
  current node; `LLMProvider` port defined with a null implementation.
- [x] First message: on session creation, evaluate the message against the root
  edges before running the root's `on_enter` (ADR 0010).
- [x] Transitions: exit actions, `goto`, entry actions, and fallback node.
- [x] Steps: `message`, `location`, and `request` with retry and result capture
  (body, headers, status, duration).
- [x] Template references: resolve `{{...}}` in message bodies and request
  URLs, headers, and bodies from the runtime context; at load, reject unknown
  roots, bad paths, and references to variables not declared in
  `initial_context` or tool `inputs`, with valid and invalid fixtures
  (ADR 0018).
- [x] Guards: cooldown and rate limit per contact per action; blocked-action
  behavior.
- [x] Hybrid intent semantics: classification fallback and rendering modes
  with anchor plus fallback (shape settled in Phase 1; ADR 0032). The policy
  check before queueing belongs to Phase 8's opt-in generation.
- [x] Tests: simulated conversations including fallback, timeout, guards,
  request failures, template reference errors, and the same input with
  different visit histories.

Gate: a fixture graph produces the expected message sequence for a scripted
conversation with no channel and no LLM.

### Phase 5 — WhatsApp channel (whatsmeow)

Goal: one real bot talks to WhatsApp end to end.

Deliverables:

- [x] whatsmeow adapter implementing the `MessageChannel` port (ADR 0004).
- [x] QR pairing, the whatsmeow device store on the shared SQLite connection
  (ADR 0006), and reconnect with capped exponential backoff plus jitter
  (ADR 0011).
- [x] Inbound event mapping to `IncomingMessage` plus deduplication.
- [x] Sender identity: resolve LID to PN via `SenderAlt` and the LID store
  before sessions and filters (ADR 0020).
- [x] Outbound send: text and location; mark inbound read and show composing
  presence before replies (ADR 0011).
- [x] Logout and ban detection: stop the bot, keep the session, and report
  through `bot list` instead of retrying forever (ADR 0011).
- [x] CLI `bot pair <bot>` and `bot list` (the command is now `bot bind`;
  ADR 0054).
- [x] Tests: adapter unit tests with fakes; a manual end-to-end checklist.

Gate: one configured bot answers a real WhatsApp message and survives a
restart without re-pairing. The rest of the system still runs without
whatsmeow; tests use a fake channel.

### Phase 6 — Fleet, filter, and outbox

Goal: multiple bots in one process, paced and isolated.

Deliverables:

- [x] Fleet supervisor that builds one actor per bot from config.
- [x] Filter: ignore groups and allow/ignore contacts (now refuse shared
  conversations and allow/ignore peers; ADR 0051); deduplication lives in
  the channel adapter, which sees the event first.
- [x] Outbox: per-bot FIFO with `queue_delay` plus a shared random `jitter`,
  an optional `max_messages_per_minute` ceiling, and backpressure (ADR 0011).
- [x] Durable delivery: replies are queued in the same transaction that
  stores them; a per-bot worker paces them, retries with bounded backoff,
  records sent or failed, and survives a restart (ADR 0040). A full inbound
  queue blocks instead of dropping the message.
- [x] Fleet command: start all bots, graceful shutdown on SIGINT/SIGTERM, and
  status reporting.
- [x] Logging: one structured logger (`log/slog`) with plain, column-aligned
  lines and no ANSI escapes, level from `log_level`, and whatsmeow forwarded.
- [x] Tests: two bots with independent sessions and queues; filter behavior;
  shutdown drains queues.

Gate: two or more bot accounts run concurrently; messages from each are
answered with the configured pacing.

### Phase 7 — CLI and REST API

Goal: operate a fleet without hand-editing config for daily operations.

Deliverables:

- [x] Package layout settled before the API: root contracts, one package per
  capability, `adapters/`, and `internal` as a last resort (ADR 0034).
- [x] Declarative objects moved to `spec`; the template language moved to the
  core and the runtime context into `engine` (ADR 0034).
- [x] CLI groups: `config init|validate|schema`, `bot list|bind|unbind`,
  `fleet run|status`, `peer list|show`, `directory list`, `chat list|sync`,
  `message list|tail`, `graph-run list`, `completion <shell>`. Operational
  queries use the API; process lifecycle belongs to the supervisor (ADR 0025).
- [x] CLI help: `usage`, description, commands, options, and examples for every
  group and subcommand; `--help` exits 0 and usage errors exit 2.
- [x] CLI `--version` and a decision on global versus per-subcommand flags.
- [x] `--json` for `bot list` and `fleet status`.
- [x] Optional: `completion` for bash. The script is rendered from the CLI
  tree and each command's own flag set, so a new command or flag cannot leave
  it stale.
- [x] `config init`: create `config_dir` with a minimal `config.yml` and a
  `config.schema.json` generated at runtime; do not overwrite without
  `--force`. Generation must not depend on the working directory: the Go doc
  comments only resolve from the module root today, so a schema generated
  elsewhere silently loses every description.
- [x] Config discovery: `--config`, then `~/.config/botflota/config.yml`, then
  `/etc/botflota/config.yml` (first match wins).
- [x] `fleet status` is an API client and reports uptime, per-bot connection
  state, outbox depth, and active sessions; a `--json` mode serves scripts.
- [x] Operational queries read the API: `bot list` asks the running instance
  for each bot's binding and connection state instead of opening the
  database and the device store (ADR 0039).
- [x] Message history persistence: the engine appends the inbound message and
  every reply under the chat and the session run that handled them (ADR 0026);
  the account-wide history sync is imported separately and inertly (ADR 0035).
- [x] WhatsApp history import (ADR 0035): capture the account-wide sync that
  arrives when an account links, in `bot bind` and in `fleet run`, storing 1:1
  content under its kind as `sync`/`unknown` with no session run; deduplicate by
  `provider_message_id`, write the channel's id back onto live replies, import
  the `PUSH_NAME` contact names, widen each chat's activity to the imported
  range, and expose `chat sync` to page older messages on demand.
- [x] Message history commands: list or tail one chat with
  `message list|tail <bot> --chat-id <id> [--limit n] [--follow]`, served through
  the REST API.
- [x] Peer identities: record each peer's display name and first/last
  inbound from the first allowed message, keyed by (scope, provider, subject)
  and shared by the scope's bots (ADR 0029). `chats.message_count` counts
  inbound messages until message history is recorded.
- [x] Peer reads over the API (ADR 0044): `GET
  /api/v1/scopes/{scope}/peers` lists the scope's identities newest activity
  first, with `since` over `first_seen` and an opaque keyset cursor, and `GET
  .../peers/{id}` answers one scope-owned identity or 404; `GET .../bots/{bot}` reads
  one bot or 404.
- [x] API-origin outbound send (ADR 0044): `POST
  /api/v1/scopes/{scope}/messages` stores and queues one text message to a
  neutral target of one `(scope, bot)` in a single transaction — resolving a
  `chat_id` under that scope and bot or addressing an explicit target — with
  `ingest=api`, `origin=api`, no session run, and
  an optional `client_id` whose repeat answers 409 with the accepted id. The
  existing outbox worker paces and retries it; the chat's control state is
  neither read nor changed; the channel's echo correlation keeps it from
  pausing the chat (ADR 0043).
- [x] First-peer webhooks (ADR 0045): `webhooks` in the main config file and
  a `PeerFirstSeenNotifier` port; `peer.first_seen` is emitted when a peer
  claims its first inbound and delivered at-least-once through a durable SQLite queue
  with exponential backoff and an HMAC-SHA256 signature over the raw body.
- [x] Graceful shutdown: SIGINT/SIGTERM stops the fleet and drains the outbox.
  Config reload is automatic and needs no signal (ADR 0009). Starting,
  stopping, and restarting are supervisor operations (ADR 0025).
- [x] REST API (`api/`): status, bots, peers, directory, chats, graph runs,
  messages, binding/setups, history, and config.
  `fleet run` serves it from the same process and injects the live fleet and
  stores; operational CLI commands call it. Handlers are small and direct; a
  narrow service is extracted only after real duplication appears.
  Every addressable object carries its scope, and a message carries its bot as
  (scope, bot) together, never a bare name: the live stream (Phase 10) and
  scoped authority (Phase 13) both route by that pair. The `config` read
  serves the typed model, not a hand-written DTO, so the Phase 8 catalogs
  (`shared`, `system_one`) surface without reworking handlers.
- [x] Scope config publication API: an owner can publish any set of existing
  scopes, and a scoped token only its authorized scopes, one or several per
  atomic request. Each scope carries its expected logical version. It writes
  validated YAML and invokes the same activation pipeline as a manual edit;
  drafts stay client-side. Optional `display_name` edits leave keys unchanged;
  absent labels stay absent in YAML. Resolve existing entities to their
  source files, put new ones in managed fragments, and edit the main file when
  it contains an authorized scope or needs an include (ADR 0002). Never edit
  unrelated scopes or global settings. This route cannot create scopes.
  Stage all affected files and publish them as one recoverable all-or-nothing
  change. Serialize overlapping file commits; rebase edits over unrelated
  scope changes in shared files, but reject the whole batch if any requested
  scope is stale, unauthorized, or invalid. Identical publishes are no-ops.
  Tests cover nested includes, concurrent scopes in one file, batch conflicts,
  API-managed definitions, and recovery after multi-file failure.
  Validate before disk writes and leave files untouched on failure. Return
  detailed validation errors to the owner and for the caller's authorized
  scope; return a generic error for invalid configuration outside that scope
  or for instance-wide failures. Document that direct file edits are not
  version-checked or serialized with API publications. See the publication
  sequence in `docs/next-plan.md`.
- [x] Visible labels: creation uses the submitted name as the YAML key and a
  later rename writes only `display_name`; a key collision is rejected, never
  auto-suffixed. Effective labels (`display_name`, else the key) must be unique
  among scopes instance-wide, within each entity category of a scope, and among
  the nodes of one graph, compared with surrounding space dropped, inner runs
  collapsed, and case ignored. Config validation enforces it, so every API
  write inherits it; a shared catalog joins the rule when its types arrive
  (ADR 0031).
- [x] Runtime/host boundary: preserve generic API publication, scopes, and
  tokens; separate host applications from optional operator tooling (ADR 0046;
  `docs/integration.md`). This documentation decision changes no runtime code.
- [ ] Scope file root (ADR 0047, not implemented): an owner-managed
  `file_roots` map in the main config file bounds the local files one scope's
  steps may read. A scope without a root keeps today's behavior; scoped
  publication cannot set it; the component that reads the bytes enforces
  confinement after templates resolve; load rejects an absolute path under a
  root and overlapping roots.
- [x] Egress policy (ADR 0048): an owner-managed `egress` instance setting.
  `public_only` refuses a non-public destination on the connection the call
  actually makes and re-checks or refuses each redirect; it covers `request`
  steps, media fetched by `url`, and webhook delivery.
- [ ] Scope secret catalog (ADR 0049, not implemented): an owner-managed
  `secret_files` map. A scope with one resolves `${NAME}` only from that file,
  which must sit outside its file root, with no fallback to the process
  environment.
- [ ] TBD — shared resource use between scopes: CPU, memory, fairness, and fetch
  bounds beyond the media cap (`docs/integration.md`).
- [ ] TBD — owner scope provisioning and grant lifecycle: decide minimal
  independent operations, naming, idempotency, and recovery before coding.
  Scope creation must not require an aggregate policy catalog.
- [ ] TBD — aggregate resource policy: decide whether it belongs in Botflota,
  exact enforcement requirements, and the smallest runtime mechanism justified
  by them. Limit sets, periods, catalogs, and policy storage are not approved
  implementation requirements (`docs/access-policy.md`). The measured envelope
  above is the evidence a bound would have to be justified against.
- [ ] TBD — technical protections: justify structural and history-import
  bounds by execution safety and settle defaults before adding config fields.
  Storage exhaustion and retention require a separate explicit decision
  (`docs/access-policy.md`).
- [x] Account history: unlinking keeps chats, messages, sessions, and
  directory; deleting one bot's history is an explicit confirmed operation that
  never touches the scope's peers or another bot and reports what it removed;
  exporting one bot's history is a streamed zip of one CSV per table the
  deletion removes, so an irreversible deletion can be an informed one;
  migration **moves** a bot's chats and directory to a bot of the same scope
  that holds no chat of its own, for a bot replacing a lost account, with both
  bots stopped, the source's queue removed, and its active sessions expired.
  ADR 0042 settles the routes, the confirmation, the report, the
  empty-destination rule, the export, the single transaction, and the retry.
- [x] Instance ownership (ADR 0039): `fleet run` and the local commands that
  write take an exclusive lock on `data_dir` for the life of the process, so a
  second run or a binding beneath a live fleet fails immediately; the port
  probe that guarded `bot bind` is gone and `port: 0` no longer hides a
  running fleet.
- [ ] Setup methods: keep the QR and add the eight-digit pairing code, so an
  account whose passkey step happens on the phone can link without another
  client; a pending step is a reported state, never a wait until the code
  expires (ADR 0028).
- [x] Setup session in the API: begin, read, answer, and cancel per bot,
  carrying the method, the typed step (display, input, approval, profile), the
  state, and the expiry, so a UI can drive onboarding without shell access; a
  session that succeeds activates the bot in the running fleet (ADR 0022,
  ADR 0025). Replacing a binding is `bot bind --force` through the API.
- [x] `bot bind` follows instance ownership (ADR 0039): it drives the running
  instance's setup session through the API — rendering its display step and
  polling until it commits — and binds locally under the lock when the fleet is
  stopped. No path falls back to the database beneath a live instance.
- [x] Account lifecycle in the API: `DELETE .../bots/{bot}/binding` detaches the
  committed binding, stops the running bot so its channel disconnects, and
  removes the adapter credentials; `bot unbind` drives it while the instance is
  owned and works locally when it is not, and `bot bind --force` replaces
  through the same path (ADR 0021, ADR 0039).
- [x] Account states beyond binding: a connection rejected with 428 (passkey
  required) or 463 (reachout timelock) stops the bot as `passkey_required` or
  `reachout_timelock` and is visible in `bot list` (ADR 0028).
- [x] Owner token auth: generated on first run, hashed in SQLite, written once
  to a `0600` file under `data_dir` for the CLI, and regenerable with
  `fleet run --new-owner-token`. Every request resolves one principal behind a
  single bearer seam and the routes stay scope-addressed.
- [ ] Deferred: session cookie for an optional operator UI. A host backend can
  use bearer tokens now; cookies and delegated sign-in do not gate integration.
- [x] Authorization base: one `Principal` per request (owner or scoped),
  scope-addressed routes, and scoped access tokens minted with the owner
  token, hashed at rest, optionally expiring, and revocable. Scope grants are
  shareable between tokens by default and exclusive where the instance sets
  `exclusive_scopes`, which also refuses startup on an overlap already stored
  (`docs/api.md`). A scoped principal requesting another scope gets 404, never
  403. This advances the scoped tokens of ROADMAP Phase 13 because the route
  shape depends on the principal.
- [x] Request budgets and opaque public errors: instance settings
  (`rate_limit`, defaults 600 and 60 per minute, `0` turns one off) budget
  each credential and each source of failed attempts (429 with
  `Retry-After`); upstream failures are answered by code and never by
  detail, and the setup session's display payload stays out of every log
  (ADR 0025).
- [x] Secret guard: the engine rejects any `${...}` that reaches an outgoing
  message (ADR 0014).
- [x] Secret policy (ADR 0014): resolve `${ENV_VAR}` only in model `api_key`
  and request header values, keep the placeholder when serializing, and
  reveal the value only to the consumer that sends it.
- [x] Reject a `${...}` that reaches any other string (ADR 0014): entities,
  instance settings (`address`, `data_dir`), `include` paths, and a scope's
  `display_name` are all scanned, and the refusal names the site.
- [x] Config watch baseline (ADR 0009): poll the main YAML and its loaded
  includes; load, interpolate, and validate a complete candidate before use.
  Invalid edits keep the active configuration.
- [x] Scope-isolated activation (ADR 0009): distinguish saved YAML from active
  runtime snapshots; calculate effective changed scopes and advance only
  those snapshots. An unchanged scope's actors, channels, filters, and queues
  remain untouched. A candidate changing several scopes is validated as one
  document and activated only where safe.
- [x] Runtime compatibility: defer graph topology or initial-context changes
  while affected bots have active sessions, retry while the saved candidate
  remains, and skip at startup rather than silently resume a session whose
  current node was removed. Preserve unrelated scopes when another scope is
  pending; keep global, scope-set, bot-set, and channel-setting changes
  explicitly restart-required.
- [x] Include watching: detect content changes, include-list changes, and new
  files matching directory or glob includes. Retry corrected invalid files
  and pending session-safe changes without requiring an unrelated edit.
- [x] Tests: invalid candidate leaves the active snapshot untouched; one-scope
  edit leaves other runtime generations untouched; active-session deferral;
  startup with a removed current node; changed or new includes; pending
  restart settings.
- [x] Tests: a multi-scope candidate, and the expiry that lifts an
  active-session deferral once those sessions end. These two close with the
  publication API, which reuses the activation pipeline
  (`docs/next-plan.md`).
- [x] Tests: API handlers and auth.
- [x] Tests: config discovery and reload behavior (validation fallback, live
  application, restart-required).
- [x] History anchors for every message kind (ADR 0035, ADR 0036): a chat whose
  synced entries carry no text still needs an anchor for `chat sync`. Every 1:1
  content entry is stored under its channel-neutral kind (text, image, video,
  audio, document, sticker, location, contact, poll, event, interactive,
  commerce, call, and an `unknown` fallback), so the chat has a provider id to
  page back from; reactions, edits, and deletions stay control-only. The same
  classifier covers live traffic, and an action may emit the sendable kinds.
- [x] Message kinds and outbound steps (ADR 0036): one channel-neutral kind
  vocabulary shared by import, live traffic, and actions; the sendable steps
  (`message`, `location`, `media`, `contact`, `poll`) are defined in the config
  schema, and a channel rejects a kind it cannot deliver with
  `ErrUnsupportedKind`.
- [x] `bot bind --param full_history=true` (ADR 0035): the parameter asks
  WhatsApp for the full history window at registration and waits for it, which is
  what brings chats older than the default recent window; a binding without it
  still imports what arrives and leaves `chat sync` as the per-chat path.
- [x] Directory import (ADR 0037): a per-bot `channel.options.import_contacts`
  copies the bound account's directory into its own `directory` resource, keyed
  by (scope, bot, peer), with the directory, business, and push names kept
  apart; `directory list` and the bot's `directory` route read it. A control-only
  conversation shell registers no peer and no chat.
- [x] `chat sync --all` (ADR 0035): after those anchors exist, request one
  bounded page per known chat over the running fleet's connection, in
  sequence and never in parallel, keeping the walk going when one chat fails.
- [x] The `chat sync --all` summary: report answered, timed-out, and skipped
  counts, and skip a chat with no anchor instead of failing the batch. A chat
  with nothing to page from answers its own public code (`no_anchor`), so the
  command counts it apart from a timeout and a failure.
- [x] Capacity envelope (a measurement, not a Phase 7 capability): an opt-in
  multi-bot load harness drives the real fleet and stores through the synthetic
  channel, and the envelope one instance is planned around is recorded —
  250–300 bots at roughly 40–50 turn commits per second, with the write path
  plateauing near 95–100 per second and device-store churn adding about a tenth
  of a core (`docs/channels/load-testing-plan.md`, section 6). ADR 0006's pool
  bound and busy timeout come from the same runs. One observation per point,
  not a capacity guarantee.

Channel-neutral preparation (`docs/channels/plan.md`; decisions in ADR
0051–0054). Every slice keeps WhatsApp runnable:

**Owner decision — no backward compatibility, clean databases
(2026-10-12, extending the 2026-10-08 clean pre-release persistence
decision).** The whole channel preparation (P1–P6) is new design: there is no
legacy to preserve and no deployment to migrate. Prefer the smallest coherent
channel-neutral design over compatibility with earlier YAML, API/CLI shapes,
templates, events, or exports. Breaking changes are the default and need no
deprecation window, dual-format period, or compatibility translation layer.

- Databases start clean. No migration, backfill, old numeric-ID preservation,
  or legacy-schema upgrade gate is required anywhere in P1–P6. Recreate an
  incompatible development database explicitly through `make dev-reset`;
  startup must not silently drop data or reset a database.
- Fresh-schema persistence, reopen/restart, atomic turns, deduplication,
  isolation, and durable delivery remain mandatory runtime gates; dropping
  upgrade tests must not drop those.
- Keeping WhatsApp operational across a phase boundary means its adapted
  implementation runs, not that its old configuration or wire shapes survive.
  A superseded key, template root, selector, or result field is removed with its
  owning phase instead of translated.
- Align the old-database and compatibility requirements in
  `docs/channels/plan.md` and ADR 0051–0054 with this
  decision. Versioned migrations and compatibility shims still begin at the
  first release, which is after this preparation.

**Owner direction — clean messaging domain (2026-10-08).** No messaging-provider-specific concept
belongs in shared binding/peer/chat contracts, even as an optional field. Do not replace
device locators with generic identity labels plus phone metadata or unchecked provider data.
`docs/channels/plan.md` owns the preparation boundaries and document map;
`docs/channels/phases.md` owns implementation and runtime gates. P1–P5 are implemented and reviewed;
P6's automated gates pass, with the WhatsApp manual checklist pending operator exercise. The
research and cleanup evidence stays in
`docs/channels/`; align the canonical ADRs before implementing any newly changed contract.
Exact selected contract, field, helper, and command names live in
`docs/channels/domain-terminology.md`; open decisions live in `docs/channels/open-decisions.md`.
The four providers are contrast cases, never a closed domain taxonomy. Use Botflota's own neutral
vocabulary and inward dependencies (ADR 0004, ADR 0034): new providers fitting existing capabilities
adapt through ports and registration, without changing common records or adding provider branches.

**Owner-approved contract review (2026-10-09).**
`docs/channels/plan.md`, section 2, refines
logical binding views versus physical resources, adapter/auxiliary credential ownership,
stable binding identity versus temporary context selection, message provenance, and receive
completion. P2–P6 add shared-backend and replay-source fakes; the P5 completion signature and
durable dedup key closed with the owner on 2026-10-17
(`docs/channels/open-decisions.md`). Service deployment, grouping, shared
overload policy, and capacity remain TBD, not a production adapter or resource-policy requirement.

Recommended minimal schema direction, subject to that canonical ADR alignment:

- Registered identities store scoped provider/subject once, with no common phone
  column or getter. A store-local provider composes its stable space into the
  opaque subject and thread it produces; the core never parses their parts.
- Chats represent conversations, keyed under scope and bot by provider, kind,
  thread, and topic. A single-peer reference is optional; a group must not
  masquerade as one peer identity.
- Sessions and messages reference the chat through foreign keys. Keep bounded
  execution state separate from durable history and sender identity separate
  from conversation identity. Provider message IDs are opaque and deduplicated
  in their actual destination space, never globally or by phone.
- Directory rows reference registered identities and record contextual membership/presentation;
  settle their binding provenance and retained-history behavior in the channel plan before coding.
- Account persistence stores a neutral account subject and, in P3, committed
  binding metadata/generation; no JID, optional phone, or QR-only account model.
  Protocol/device lookups, credentials, and SDK-owned tables remain adapter-owned.
  Safe presentation and legacy formats do not become domain identity fields.
- Outbox rows retain the exact destination and accepted binding generation,
  including alternate recipients; a source message's chat is not necessarily
  its delivery target. Keep intentional delivery snapshots, not legacy aliases.
- Enforce identity uniqueness, scope-safe relationships, and required values
  with SQL constraints where practical. Index existing lookup and queue paths;
  avoid duplicate columns, redundant indexes, and normalization for its own sake.

- [x] P0 — initial decisions and documented reference cases: identity,
  conversations, setup, capabilities, and compatibility accepted; WhatsApp, Telegram-token,
  user-login, and local-profile cases described without a fake phone, a
  universal QR, a leaked credential, or a global address.
- [x] P1 — identity and target propagation (ADR 0051): the identity/directory
  phone/JID cleanup and explicit participant mapping landed across the core,
  SQLite stores, engine, fleet, WhatsApp mapping, and the API/CLI/webhook
  consumers. The schema is destination-keyed and created fresh; a provider id is
  deduplicated in its actual destination; and the gate closes/reopens the
  database and reads the persisted conversation with no sender cache. P1 retired
  the `contact` template root without a universal phone field and with no
  compatibility translation; only the neutral `peer.*` values remain, and no
  legacy backfill or pre-change database gate is required.
- [x] P2 — declared capabilities and channel configuration (ADR 0052, ADR
  0054): the injected catalog, typed selector, pure support checks, reload
  comparison, and composition; a bot names its channel explicitly and a fake
  text-only channel needs no WhatsApp device store. No compatibility layer,
  legacy settings map, or template alias survives; the selector is required and
  adapter settings live only in `channel.options`.
  Completed 2026-10-12: `channels` holds the catalog and definitions, config,
  schema, startup, watcher, and publication share the pure catalog, each bot's
  graph is checked against its own definition, the WhatsApp adapter owns its
  options, card variants, readiness, and builder, and the device store opens
  only for a selected adapter. Its gate is checked in
  `docs/channels/phases.md`.
  A future `invite` step (label plus opaque link or `self` plus kind `peer`|`group`)
  is decided but gated on a first real consumer; P5 admission/delivery checks apply
  once it lands.
- [x] P3 — binding runtime (ADR 0053): neutral association metadata, the
  inspection/bind/unbind operations, and the WhatsApp adaptation, with QR,
  write-only credential, multi-step approval, and profile binding modeled by
  fakes. Persist binding generation and identity-space context; remove common
  phone-bearing pairing results and phone-specific ownership/error semantics
  instead of translating them.
  Completed 2026-10-12: committed `Binding` metadata with generations,
  neutral setup sessions with revisions and request keys, definition-selected
  inspection/detach, and the WhatsApp translation over the same machinery.
  No migration, backfill, or legacy result shape survives; fresh-schema
  persistence, reopen, and detach-keeps-history gates pass. Its gate is
  checked in `docs/channels/phases.md`.
- [x] P4 — remote API and CLI integration (ADR 0053): public-contract decisions
  are owner-approved on 2026-10-12 (`docs/channels/open-decisions.md`). Binding
  operations and discovery use scoped routes; a scoped HTTP client completes
  binding and reauthorization without filesystem access. Neutral views,
  webhooks, and exports carry no universal phone field and no legacy output
  projection. Completed 2026-10-17: neutral scoped API/CLI routes and views,
  the binding/setup lifecycle, binding compatibility before history migration,
  and the HTTP end-to-end gate. Its gate is checked in
  `docs/channels/phases.md`.
- [x] P5 — delivery and optional-operation semantics (ADR 0052): outbox
  failure classes, binding-generation checks, and normalized
  history/external-reply contracts; a replacement account cannot send the
  previous account's pending entries (delivery, reception, and provenance
  contracts closed on 2026-10-17). Enforce declared support and dynamic
  eligibility for P2's card payload, with no phone rules in shared delivery.
  Completed 2026-10-17: classified send outcomes, binding-generation delivery
  gates, admission and delivery recheck, the message-ID space and dedup key,
  receive/processing completion and replay, channel lifetime,
  history/external-reply normalization, and shared-source backpressure. Every
  item and gate is checked in `docs/channels/phases.md`.
- [ ] P6 — regression gate and documentation: glossary, architecture, API,
  config examples/schema, and guides match the code; `make check`,
  `make test-race` where concurrency changed, and the WhatsApp manual
  checklist pass. The boundary audit in `docs/channels/phases.md` P6 passes,
  including optional fields, template context, and read/event models.
  Implementation and automated gates complete 2026-10-17, but the WhatsApp
  manual checklist is pending operator exercise, so P6 remains open; its
  automated gates are checked in `docs/channels/phases.md`.

Gate: a fresh instance can be validated, authenticated, started, inspected,
and reconfigured through CLI and API only. Scoped publication of several
authorized scopes is atomic and leaves every other scope untouched. An owner
can delete one bot's history and migrate it to another bot of the same scope
without touching the scope's peers or another bot. An integration can send a
message to an eligible channel target, read the scope's peers, and receive a
signed `peer.first_seen` webhook. The channel-neutral slices (ADR 0051–0054)
keep WhatsApp runnable
and close with their own gates (`docs/channels/plan.md`).

Provisioning and resource policy remain TBD in `docs/access-policy.md` and do
not gate the outcome above. An owner can bound a definition's local files (ADR
0047), outbound HTTP (ADR 0048), and a scope's secret names (ADR 0049); shared
resource use stays TBD (`docs/integration.md`). Scope authorization alone is not
a sandbox.

### Phase 8 — System One routing, replies, and tools

Goal: System One becomes the primary AI path, prescribed replies are the
default, and a generative LLM is an explicit opt-in for reading text and
writing replies.

Deliverables:

- [ ] Reconcile the interim contracts with ADR 0004 and ADR 0030 first: drop
  `LLMProvider.Classify` (only `SystemOneProvider` decides transitions),
  build the providers from config — `fleet` injects none today — and either
  reject or clearly mark the config fields no runtime reads yet (`system_one`,
  `shared`, `routing` on an `llm` profile, `tools`).
- [ ] `SystemOneProvider` port and a Jev (TypeSafe) adapter: typed questions
  with confidence, and the deterministic matcher first (ADR 0030).
- [ ] `shared` and scope catalogs (`system_one`, `llms`): config types,
  generated schema, `config/examples/reference.yml`, fixtures, and validation,
  with a name resolving in the scope first and then in `shared` (ADR 0031).
- [ ] Confidence-gated routing: a per-graph threshold decides between taking
  the decision, the default edge, or escalation, and is logged per message.
- [ ] Per-graph routing override: `off` | `fallback` | `always` (ADR 0030),
  applied to System One; `always` gives it the decision even when a trigger
  matches.
- [ ] Prescribed replies as the default path: `body` messages with variants
  chosen at random, and no provider required (ADR 0032).
- [ ] Tool pipeline (ADR 0033): intake gate, LLM extraction of candidate
  parameters validated against `inputs`, selection with one `Noul` per tool
  plus an explicit `none`, mapping of values to catalog entries with a
  `Choice`, and confidence thresholds.
- [ ] Staying in the node: a handled tool message takes no edge and does not
  re-run entry actions, and a tool node declares explicit exits instead of a
  catch-all default.
- [ ] Confirmation before side effects: the pipeline proposes, a prescribed
  message confirms, and only then the tool writes.
- [ ] Closed-set inputs: `inputs` declares the values System One maps against,
  with staged mapping for catalogs beyond the `Choice` cardinality.
- [ ] Numbers and dates parsed in code from the literal span; models never
  compute them.
- [ ] Opt-in generation (ADR 0032): `LLMProvider` implementations for reply
  realization only (one cloud and one local), `body` plus `intent` anchored
  and used as fallback, and policy checks before queueing.
- [ ] Opt-in LLM tool loop (ADR 0033) for nodes whose calls a pipeline cannot
  bound, sharing `inputs` validation and the edge rule.
- [ ] Slot filling: a missing required input returns a tool error or asks the
  user, and bound variables remember the answer; `bind` targets are validated
  against node and graph `initial_context`.
- [ ] Async APIs: LLM-driven polling within the tool budget (call, check
  status, retry); longer waits continue on later messages.
- [ ] Deterministic fallback when no provider is configured or a call fails:
  routing degrades to triggers and the default edge.
- [ ] Bounded calls: a timeout from the `system_one` `bounds`, and the tool
  loop limits (max tools per message, timeout) from the `llms` `bounds`.
- [ ] Per-message logging of node, intent, System One model (SOM), question,
  answer, confidence, tools called, provider, and policy version.
- [ ] Tests: provider fakes, decision cases with confidence thresholds, config
  resolution across scope and `shared`, multi-select and mapping cases,
  extraction validated against `inputs`, tool argument validation, policy
  rejection fallback, and evaluation fixtures (golden conversations).

Gate: a graph runs deterministically, with System One routing plus prescribed
replies, and with the tool pipeline handling a multi-message tool node, with
logs that explain each decision. The generative path is optional.

### Phase 9 — MVP hardening and release

Goal: an MVP someone can run from a clean machine.

Deliverables:

- [ ] Reference bot: a small support menu graph with intents and one `request`
  step.
- [x] systemd integration (ADR 0025): user and system unit templates plus
  `scripts/install-service.sh` (`make install-service`,
  `make install-service-system`) that write the unit, create the config and a
  `0600` secrets file, and enable and start `fleet run`. The unit reads secrets
  through an `EnvironmentFile`, restarts on failure, and gives the paced outbox
  a stop timeout to drain. The binary never invokes systemd; the installer is
  packaging.
- [ ] Container setup, backup of SQLite and the device store, and
  account-safety practices (warmup for new accounts, no bulk outreach, human
  fallback when a bot is restricted) (ADR 0011). Document that accounts
  requiring a passkey on every connection cannot be automated (ADR 0028).
- [ ] Log and error review: actionable messages, no secrets in logs.
- [ ] Documentation pass: README, scope, and architecture match the code.
- [ ] Schema baseline: freeze the current schema as migration 1 and add the
  versioned, append-only migration runner (ADR 0006).
- [ ] Release checklist and version tag.

Gate: a clean-machine run of the documented path works without tribal
knowledge.

## Deferred (post-MVP)

`ROADMAP.md` owns runtime work after Phase 9: operational reads and events,
optional operator tooling, remaining controls, credentials, integration,
additional channels, and the script step. Visual authoring, assisted onboarding,
and application-level evolution are outside its commitments (ADR 0046).

## Open decisions

- Schema generator choice and approval (Phase 1; candidate:
  `invopop/jsonschema`).
- Structured policy shape and conflict precedence (Phase 4; free string now).
- Tool result template syntax (close in Phase 4/8).
- Deterministic polling step shape for async APIs: interval, timeout,
  success condition (Phase 4/8).
- Context variable typing and empty-versus-default semantics (Phase 4).
- Confirmation guard for tools with side effects (Phase 8).
- Adopt TypeSafe Jev as the first System One implementation (early access):
  confirm access and pricing before Phase 8 starts (ADR 0030).
- System One confidence threshold: default and per-graph override (Phase 8).
- Closed-set inputs: how `inputs` declares the values for mapping, and the
  staging threshold for large catalogs (Phase 8; ADR 0033).
- Whether extraction runs on every tool message or only when the intake gate
  asks for it (Phase 8; ADR 0033).
- Selecting a reply variant with a decision, or separate nodes for now
  (Phase 8; ADR 0032).
- Whether `shared` may ever carry `actions` or `graphs`, and the governance
  that would need (Phase 8/15).
- How export and import inline a scope's reference to `shared` (Phase 18).
- LLM bounds defaults: max tools per message and timeout (Phase 8).
- Which LLM providers ship first for realization (close in Phase 8).
- TBD — resource policy, usage durability, periods, storage exhaustion, and
  provisioning contracts (Phase 7; `docs/access-policy.md`).
- TBD — shared resource use between scopes: CPU, memory, and fairness
  (`docs/integration.md`).
- Implementation: scope file root (ADR 0047) and scope secret catalog
  (ADR 0049) — `PLAN.md`, Phase 7.
- TBD — implicit scope for simple YAML: whether to add root-level entities,
  ambiguity rules with explicit scopes/includes, and stable persisted identity.
  ADR 0016 still requires explicit scopes; `default` is an ordinary name.
- TBD — technical protection defaults: nodes per graph, edges per node, steps
  and inputs per action, and bounded history imports (`docs/access-policy.md`).
- Trusted boundary for a managed local channel endpoint (for example a SimpleX
  websocket) before one is added (Phase 7; ADR 0054).

Resolved: Go structs are the schema source and runtime validation happens in
Go (ADR 0007); the Go version and CLI approach are in ADR 0012, and test
conventions are in `AGENTS.md`. The config baseline settles in Phase 1 and
keeps evolving until the MVP release. The file carries no version field; the
binary's version identifies the format it implements (ADR 0041).
The operational CLI is a client of the REST API, authenticated with the owner
token or a scoped access token, and `config init` is non-interactive (Phase 7,
ADR 0025).
The runtime/host boundary is settled in ADR 0046. Resource policy and
provisioning remain TBD in `docs/access-policy.md`; its candidate design is not
an accepted contract. History lifecycle is separately settled in ADR 0042.
Channel-neutral identity, capabilities, remote account setup, and channel
configuration are settled in ADR 0051–0054 (Phase 7).
Trigger matching and its threshold are in ADR 0019. AI routing is
decision-only via System One (ADR 0030), model catalogs are scoped with a
shared host catalog (ADR 0031), prescribed replies are the default (ADR
0032), and tools run through the pipeline of ADR 0033, where verification is a
System One role.

Dependencies in use: `go.yaml.in/yaml/v3` (the maintained successor of
`gopkg.in/yaml.v3`), `github.com/invopop/jsonschema`, the pure-Go SQLite
driver `modernc.org/sqlite` (ADR 0006), `go.mau.fi/whatsmeow` (ADR 0001), and
`github.com/mdp/qrterminal` for the ASCII pairing QR. `urfave/cli/v3` and
`google/jsonschema-go` are no longer needed: the CLI uses the standard
library (ADR 0012) and runtime validation is in Go (ADR 0007).
