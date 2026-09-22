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

### Phase 1 — Config engine, config v1, and `config validate`

Goal: settle the config v1 baseline and turn YAML into a validated typed model
with precise errors. The baseline keeps evolving through the MVP; v1 freezes at
release.

Deliverables:

- [x] Design task first: settle the config v1 baseline (node `goal`/`policy`/`tools`,
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
- [x] Contexts: graph, node, sender, message, bot, and request results.
- [x] Read/write versus read-only context rules; node context resets on re-entry.
- [x] Tests: resolution, missing paths, escaping, and request result capture.

Gate: message steps render correctly from a simulated context.

### Phase 3 — Persistence (SQLite)

Goal: runtime state survives restarts.

Deliverables:

- [x] SQLite setup with the schema applied on open; pure-Go driver, WAL,
  foreign keys, busy timeout, and synchronous=NORMAL (ADR 0006).
- [x] Stores: chats, sessions, and messages.
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
- [x] Guards: cooldown and rate limit per sender per action; blocked-action
  behavior.
- [x] Hybrid intent semantics: classification fallback, rendering modes with
  anchor plus fallback, and policy checks (shape settled in Phase 1;
  ADR 0032).
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
- [x] CLI `bot pair <bot>` and `bot list`.
- [x] Tests: adapter unit tests with fakes; a manual end-to-end checklist.

Gate: one configured bot answers a real WhatsApp message and survives a
restart without re-pairing. The rest of the system still runs without
whatsmeow; tests use a fake channel.

### Phase 6 — Fleet, filter, and outbox

Goal: multiple bots in one process, paced and isolated.

Deliverables:

- [x] Fleet supervisor that builds one actor per bot from config.
- [x] Filter: ignore groups and allow/ignore senders; deduplication lives in
  the channel adapter, which sees the event first.
- [x] Outbox: per-bot FIFO with `queue_delay` plus a shared random `jitter`,
  an optional `max_messages_per_minute` ceiling, and backpressure (ADR 0011).
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
- [x] CLI groups: `config init|validate|schema`, `bot list|pair|unpair`,
  `fleet run|status`, `chat list|sync`, `message list|tail`. Operational queries
  use the API; process lifecycle belongs to the supervisor (ADR 0025).
- [x] CLI help: `usage`, description, commands, options, and examples for every
  group and subcommand; `--help` exits 0 and usage errors exit 2.
- [x] CLI `--version` and a decision on global versus per-subcommand flags.
- [x] `--json` for `bot list` and `fleet status`.
- [ ] Optional: `completion` for bash, zsh, and fish.
- [x] `config init`: create `config_dir` with a minimal `config.yml` and a
  `config.schema.json` generated at runtime; do not overwrite without
  `--force`. Generation must not depend on the working directory: the Go doc
  comments only resolve from the module root today, so a schema generated
  elsewhere silently loses every description.
- [x] Config discovery: `--config`, then `~/.config/botflota/config.yml`, then
  `/etc/botflota/config.yml` (first match wins).
- [x] `fleet status` is an API client and reports uptime, per-bot connection
  state, outbox depth, and active sessions; a `--json` mode serves scripts.
- [x] Message history persistence: the engine appends the inbound message and
  every reply under the chat and the session run that handled them (ADR 0026);
  the pairing history sync is imported separately and inertly (ADR 0035).
- [x] WhatsApp history import (ADR 0035): capture the account-wide sync that
  arrives when an account links, in `bot pair` and in `fleet run`, storing 1:1
  content under its kind as `sync`/`unknown` with no session run; deduplicate by
  `provider_message_id`, write the channel's id back onto live replies, import
  the `PUSH_NAME` contact names, widen each chat's activity to the imported
  range, and expose `chat sync` to page older messages on demand.
- [x] Message history commands: list or tail one chat with
  `message list|tail <bot> <sender> [--limit n] [--follow]`, served through
  the REST API.
- [x] Sender profiles: record each sender's push name and first/last seen from
  the first allowed message, keyed by (scope, phone) and shared by the scope's
  bots (ADR 0029). `chats.message_count` counts inbound messages until message
  history is recorded.
- [x] Graceful shutdown: SIGINT/SIGTERM stops the fleet and drains the outbox.
  Config reload is automatic and needs no signal (ADR 0009). Starting,
  stopping, and restarting are supervisor operations (ADR 0025).
- [x] REST API (`api/`): status, bots, chats, sessions, messages, and config.
  `fleet run` serves it from the same process and injects the live fleet and
  stores; operational CLI commands call it. Handlers are small and direct; a
  narrow service is extracted only after real duplication appears.
  Every addressable object carries its scope, and a message carries its bot as
  (scope, bot) together, never a bare name: the live stream (Phase 10) and
  scoped authority (Phase 13) both route by that pair. The `config` read
  serves the typed model, not a hand-written DTO, so the Phase 8 catalogs
  (`shared`, `system_one`) surface without reworking handlers.
- [ ] Pairing methods: keep the QR and add the eight-digit pairing code, so an
  account whose passkey step happens on the phone can link without another
  client; a pending step is a reported state, never a wait until the code
  expires (ADR 0028).
- [ ] Pairing session in the API: start, cancel, and status per bot, carrying the
  method, the payload (QR or code), the state, and the expiry, so a UI can drive
  onboarding without shell access.
- [ ] Account states beyond pairing: a connection rejected with 428 (passkey
  required) or 463 (reachout timelock) stops the bot and is visible in
  `bot list` (ADR 0028).
- [x] Owner token auth: generated on first run, hashed in SQLite, written once
  to a `0600` file under `data_dir` for the CLI, and regenerable with
  `fleet run --new-owner-token`. Every request resolves one principal behind a
  single bearer seam and the routes stay scope-addressed.
- [ ] Session cookie for the future UI, so Phase 13 replaces the resolver
  instead of every handler.
- [x] Authorization base: one `Principal` per request (owner or scoped),
  scope-addressed routes, and scoped access tokens minted with the owner
  token, hashed at rest, optionally expiring, and revocable. A scoped
  principal requesting another scope gets 404, never 403. This advances the
  scoped tokens of ROADMAP Phase 13 because the route shape depends on the
  principal.
- [x] Secret guard: the engine rejects any `${...}` that reaches an outgoing
  message (ADR 0014).
- [x] Secret policy (ADR 0014): resolve `${ENV_VAR}` only in model `api_key`
  and request header values, reject it in every other string, keep the
  placeholder when serializing, and reveal the value only to the consumer that
  sends it.
- [ ] Hot reload (ADR 0009): watch the main file and its includes, replace the
  config atomically only after full validation, apply safe changes immediately,
  and log a restart-required warning for the rest. No pending queue or
  maintenance mode.
- [x] Tests: API handlers and auth.
- [ ] Tests: config discovery and reload behavior (validation fallback, live
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
- [ ] `bot pair --import-history` (ADR 0035): the flag asks WhatsApp for the
  full history window at registration and waits for it, which is what brings
  chats older than the default recent window; a pairing without it still
  imports what arrives and leaves `chat sync` as the per-chat path.
- [ ] Contact import (ADR 0037): a per-bot `settings.import_contacts` copies the
  linked account's address book into its own `contacts` resource, keyed by
  (scope, bot, phone), with the address-book, business, and push names kept
  apart; `contact list` and the scope `contacts` route read it. A control-only
  conversation shell registers no sender and no chat.
- [ ] `chat sync --all` (ADR 0035): after those anchors exist, request one
  bounded page per known chat over the running fleet's connection, in
  sequence and never in parallel, skipping chats without an anchor and
  reporting answered, timed-out, and skipped counts in the summary.

Gate: a fresh instance can be validated, authenticated, started, inspected,
and reconfigured by editing its files, through CLI and API only.

### Phase 8 — System One routing, replies, and tools

Goal: System One becomes the primary AI path, prescribed replies are the
default, and a generative LLM is an explicit opt-in for reading text and
writing replies.

Deliverables:

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
- [ ] Per-message logging of node, intent, System One profile, question,
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
- [ ] Operational notes: systemd unit and container setup, native supervisor
  commands for start/stop/restart, restart policies, and SIGTERM graceful
  shutdown (ADR 0025); backup of SQLite and the device store; and
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

`ROADMAP.md` owns everything after Phase 9: the dashboard, scoped authority,
the editor, the onboarding assistant, autonomous evolution, the script step,
and additional channels, each with its own phase, order, and open decisions.

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

Resolved: Go structs are the schema source and runtime validation happens in
Go (ADR 0007); the Go version and CLI approach are in ADR 0012, and test
conventions are in `AGENTS.md`. The config v1 baseline settles in Phase 1 and
keeps evolving until the MVP release.
The operational CLI is a client of the REST API, authenticated with the owner
token or a scoped access token, and `config init` is non-interactive (Phase 7,
ADR 0025).
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
