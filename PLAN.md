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
7. LLM providers and hybrid behavior
8. CLI and REST API
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

Goal: freeze the config v1 shape and turn YAML into a validated typed model
with precise errors.

Deliverables:

- [x] Design task first: freeze config v1 (node `goal`/`policy`/`tools`,
  action `inputs`/`bind`, edge `intent`/`triggers`, message modes, scope,
  graph `routing`, `llms`). Align `config/example.yml` and the draft
  `config.schema.json` with it.
- [x] Domain types: Action, Graph, Node, Edge, Step, Bot, Settings, Guard,
  LLM. They live in `internal/config` for now; split out when the engine
  needs a YAML-free domain.
- [x] Loader: main config plus `include` fragments (file, glob, directory,
  recursive), scope merging, and cycle-safe dedupe (ADR 0016).
- [x] Runtime validation in Go: strict YAML decoding plus semantic checks.
  The JSON Schema is generated from the Go types for editors only (ADR 0007).
- [x] Environment interpolation: expand `${ENV_VAR}` in config strings after
  decoding; missing variables fail validation (ADR 0014).
- [x] Secret values use a type that masks itself in logs and errors
  (ADR 0014).
- [x] Semantic validation: reference resolution, scope isolation, uniqueness by
  (type, scope, name), node reachability, and message mode rules.
- [x] Schema generation: pick and approve a generator (candidate:
  `invopop/jsonschema`) and wire the regeneration step.
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
  anchor plus fallback, and policy checks (shape frozen in Phase 1;
  ADR 0003).
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
  before sessions and filters (ADR 0013).
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

- [ ] CLI groups: `config init|validate|schema`, `bot list|pair|unpair`,
  `fleet run|status|stop|restart|reload`, `message list|tail`.
- [x] CLI help: `usage`, description, commands, options, and examples for every
  group and subcommand; `--help` exits 0 and usage errors exit 2.
- [ ] CLI `--version` and a decision on global versus per-subcommand flags.
- [ ] `--json` for `bot list` and `fleet status`.
- [ ] Optional: `completion` for bash, zsh, and fish.
- [ ] `config init`: create `config_dir` with a minimal `config.yml` and a
  `config.schema.json` generated at runtime; do not overwrite without
  `--force`. Generation must not depend on the working directory: the Go doc
  comments only resolve from the module root today, so a schema generated
  elsewhere silently loses every description.
- [ ] Config discovery: `--config`, then `~/.config/botflota/config.yml`, then
  `/etc/botflota/config.yml` (first match wins).
- [ ] `fleet status` reports uptime, per-bot connection state, outbox depth,
  and active sessions; a `--json` mode serves scripts.
- [ ] When the fleet API exists, `fleet status` prefers its live data and
  falls back to the database; the output identifies the source as `live` or
  `stored`.
- [ ] Message history: persist every message under its chat as it flows
  through the actor, and list or tail one chat with
  `message list|tail <bot> <sender> [--limit n] [--follow]`. Only live traffic
  is recorded; the history-sync backlog stays out of scope.
- [ ] Signals: SIGINT/SIGTERM graceful shutdown; `fleet stop` signals the
  running fleet, `fleet reload` (SIGHUP) reloads config, and `fleet restart`
  does a full restart.
- [ ] REST API: status, bots, chats, sessions, messages, and config. Every
  addressable object carries its scope, and a message carries its bot as
  (scope, bot) together, never a bare name: the live stream (Phase 10) and
  scoped authority (Phase 13) both route by that pair.
- [ ] Bot service: move what the CLI and the API share out of `cmd/` (pairing
  pre-checks, scope disambiguation, bot status) into a package, so the API
  does not reimplement it.
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
- [ ] Auth: single admin key, hashed at rest, regenerable; session cookie for
  the future UI. Resolve one principal per request behind a single seam and
  keep routes scope-addressed, so the scoped authority of Phase 13 replaces
  the resolver instead of every handler.
- [ ] Config reload (ADR 0009): failed validation keeps the previous config;
  safe changes apply immediately; incompatible changes wait in a pending
  queue for maintenance mode.
- [ ] Tests: API handlers, auth, config discovery, and both reload classes.

Gate: a fresh instance can be validated, authenticated, started, inspected,
and reloaded through CLI and API only.

### Phase 8 — LLM providers, tools, and hybrid behavior

Goal: the LLM-first design becomes usable without making LLMs mandatory.

Deliverables:

- [ ] `LLMProvider` implementations: at least one cloud provider and one local
  option, selected by config.
- [ ] Intent classification as fallback for unmatched input.
- [ ] Per-graph routing override: `off` | `fallback` | `always` (ADR 0015);
  `always` gives the LLM the decision even when a trigger matches.
- [ ] Tool calling in nodes (ADR 0008): the LLM invokes node `tools` with
  arguments validated against their `inputs`, chains calls, and stays in or
  leaves the node through declared edges only.
- [ ] Slot filling: missing required inputs return a tool error; the LLM asks
  the user and bound variables remember the answer. `bind` targets are
  validated against node and graph `initial_context`.
- [ ] Reply realization within node policy; `body` plus `intent` uses the body
  as anchor and fallback; policy checks before queueing.
- [ ] Deterministic fallback when no provider is configured or a call fails.
- [ ] Bounded tool loop: max tools per message and timeout from the llm
  `bounds`.
- [ ] Async APIs: LLM-driven polling within the tool budget (call, check
  status, retry); longer waits continue on later messages.
- [ ] Per-message logging of node, intent, tools called, provider, and policy
  version.
- [ ] Tests: provider fakes, classification cases, tool argument validation,
  policy rejection fallback, and evaluation fixtures (golden conversations).

Gate: the same graph runs with no LLM (deterministic) and with an LLM
(routing, realization, tool use, or a combination), with logs that explain
each decision.

### Phase 9 — MVP hardening and release

Goal: an MVP someone can run from a clean machine.

Deliverables:

- [ ] Reference bot: a small support menu graph with intents and one `request`
  step.
- [ ] Operational notes: systemd unit, backup of SQLite and the device store,
  and account-safety practices (warmup for new accounts, no bulk outreach,
  human fallback when a bot is restricted) (ADR 0011). Document that accounts
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
- LLM bounds defaults: max tools per message and timeout (Phase 8).
- Which LLM providers ship first (close in Phase 8).
- Whether the CLI acts as a client of the REST API for fleet operations
  (Phase 7).
- API shape and auth details (close in Phase 7).
- `config init` behavior: non-interactive minimum (recommended) or prompting for
  provider and model (Phase 7).

Resolved: Go structs are the schema source and runtime validation happens in
Go (ADR 0007); the Go version and CLI approach are in ADR 0012, and test
conventions are in `AGENTS.md`. The config v1 shape is frozen in Phase 1.
Trigger matching and its threshold are in ADR 0019.

Dependencies in use: `go.yaml.in/yaml/v3` (the maintained successor of
`gopkg.in/yaml.v3`), `github.com/invopop/jsonschema`, the pure-Go SQLite
driver `modernc.org/sqlite` (ADR 0006), `go.mau.fi/whatsmeow` (ADR 0001), and
`github.com/mdp/qrterminal` for the ASCII pairing QR. `urfave/cli/v3` and
`google/jsonschema-go` are no longer needed: the CLI uses the standard
library (ADR 0012) and runtime validation is in Go (ADR 0007).
