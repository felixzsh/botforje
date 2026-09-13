# BotForje — Plan

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
1. Config engine and `validate`
2. Templates and context
3. Persistence (SQLite)
4. Graph engine
5. WhatsApp channel (whatsmeow)
6. Fleet, inbox, outbox, daemon
7. LLM providers and hybrid behavior
8. CLI and REST API
9. MVP hardening and release

## Phases

### Phase 0 — Foundations

Goal: repository ready to receive code, with the riskiest dependency tested.

Deliverables:

- [x] Documentation set: `README.md`, `PLAN.md`, `docs/scope.md`,
  `docs/glossary.md`, `docs/architecture.md`, `docs/adr/`, `AGENTS.md`.
- [x] Toolchain decisions recorded: Go version, CLI approach, test layout
  (ADR 0012).
- [x] Time-boxed spike: pair a WhatsApp account with whatsmeow, persist the
  device store, receive and send one message. Throwaway code, not merged.
- [x] Test conventions: table-driven tests and fixture layout (ADR 0012).

Gate: the spike proves pairing and messaging work; conventions are written.

Out of scope: any product code.

### Phase 1 — Config engine, config v1, and `validate`

Goal: freeze the config v1 shape and turn YAML into a validated typed model
with precise errors.

Deliverables:

- [ ] Design task first: freeze config v1 (node `goal`/`policy`/`tools`,
  action `inputs`/`bind`, edge `intent`/`triggers`, message modes, scope,
  graph `routing`, `llm` section). Align `config.example.yaml` and the draft
  `config.schema.json` with it.
- [ ] Domain types: Action, Graph, Node, Edge, Step, Bot, Settings, Guard, LLM.
- [ ] Loader: main config plus `include` fragments (file, glob, directory),
  merge, and scope handling.
- [ ] Runtime validation in Go: strict YAML decoding plus semantic checks.
  The JSON Schema is generated from the Go types for editors only (ADR 0007).
- [ ] Semantic validation: reference resolution, scope isolation, uniqueness by
  (type, scope, name), node reachability, and message mode rules.
- [ ] Schema generation: pick and approve a generator (candidate:
  `invopop/jsonschema`) and wire the regeneration step.
- [ ] CLI command `validate` with readable output and a `--json` mode.
- [ ] Fixtures: valid and invalid cases for every rule.

Gate: `validate` accepts valid fixtures and rejects every invalid fixture
with an error naming file, entity, and reason. Unit tests for merge and
semantic validation; integration tests over fixtures.

### Phase 2 — Templates and context

Goal: `{{...}}` values resolve correctly in messages and requests.

Deliverables:

- [ ] Syntax and resolver for `{{path.to.value}}`.
- [ ] Contexts: graph, node, sender, message, bot, and request results.
- [ ] Read/write versus read-only context rules; node context resets on re-entry.
- [ ] Tests: resolution, missing paths, escaping, and request result capture.

Gate: message steps render correctly from a simulated context.

### Phase 3 — Persistence (SQLite)

Goal: runtime state survives restarts.

Deliverables:

- [ ] SQLite setup with migrations from the first table; pure-Go driver, WAL,
  foreign keys, busy timeout, and synchronous=NORMAL (ADR 0006).
- [ ] Stores: whatsmeow device store, sessions, conversations, messages.
- [ ] Repository interfaces owned by the domain; SQL stays in the adapter.
- [ ] Tests: migrations, store round-trips, and session resume after restart.

Gate: a session persisted by one process resumes in a new process.

### Phase 4 — Graph engine

Goal: conversations run fully in memory, testable with no channel.

Deliverables:

- [ ] Session lifecycle: create at root, resume, and expire by timeout.
- [ ] Node memory: ordered list of visited nodes (current last), persisted and
  never reset by navigation (ADR 0010).
- [ ] Routing: candidates are visited nodes plus the current node (last); best
  trigger match wins, ties go to the most recent. Default edge only from the
  current node; `LLMProvider` port defined with a null implementation.
- [ ] First message: on session creation, evaluate the message against the root
  edges before running the root's `on_enter` (ADR 0010).
- [ ] Transitions: exit actions, `goto`, entry actions, and fallback node.
- [ ] Steps: `message`, `location`, and `request` with retry and result capture
  (body, headers, status, duration).
- [ ] Guards: cooldown per sender per action; blocked-action behavior.
- [ ] Hybrid intent semantics: classification fallback, rendering modes with
  anchor plus fallback, and policy checks (shape frozen in Phase 1;
  ADR 0003).
- [ ] Tests: simulated conversations including fallback, timeout, guards,
  request failures, and same input with different visit histories.

Gate: a fixture graph produces the expected message sequence for a scripted
conversation with no channel and no LLM.

### Phase 5 — WhatsApp channel (whatsmeow)

Goal: one real bot talks to WhatsApp end to end.

Deliverables:

- [ ] `MessageChannel` port and the whatsmeow adapter behind it.
- [ ] QR pairing, device store persistence, and reconnect with capped
  exponential backoff plus jitter (ADR 0011).
- [ ] Inbound event mapping to `IncomingMessage` plus deduplication.
- [ ] Sender identity: resolve LID to PN via `SenderAlt` and the LID store
  before sessions and filters (ADR 0013).
- [ ] Outbound send: text and location; mark inbound read and show composing
  presence before replies (ADR 0011).
- [ ] Logout and ban detection: stop the bot, keep the session, and report
  through `status` instead of retrying forever (ADR 0011).
- [ ] CLI `auth <bot>` and `status`.
- [ ] Tests: adapter unit tests with fakes; a manual end-to-end checklist.

Gate: one configured bot answers a real WhatsApp message and survives a
restart without re-pairing. The rest of the system still runs without
whatsmeow; tests use a fake channel.

### Phase 6 — Fleet, inbox, outbox, daemon

Goal: multiple bots in one process, paced and isolated.

Deliverables:

- [ ] Fleet supervisor that builds one actor per bot from config.
- [ ] Inbox: routing, dedup, and filters (ignore groups, allow/ignore senders).
- [ ] Outbox: per-bot FIFO with `queue_delay` plus random `queue_jitter`, an
  optional `max_messages_per_minute` ceiling, and backpressure (ADR 0011).
- [ ] Daemon command: start all bots, graceful shutdown on SIGINT/SIGTERM, and
  status reporting.
- [ ] Tests: two bots with independent sessions and queues; filter behavior;
  shutdown drains queues.

Gate: two or more bot accounts run concurrently; messages from each are
answered with the configured pacing.

### Phase 7 — LLM providers, tools, and hybrid behavior

Goal: the LLM-first design becomes usable without making LLMs mandatory.

Deliverables:

- [ ] `LLMProvider` implementations: at least one cloud provider and one local
  option, selected by config.
- [ ] Intent classification as fallback for unmatched input.
- [ ] Per-graph routing override: `off` | `fallback` | `always`; `always` gives
  the agent the decision even when a trigger matches.
- [ ] Agentic tool calling (ADR 0008): the agent invokes node `tools` with
  arguments validated against their `inputs`, chains calls, and stays in or
  leaves the node through declared edges only.
- [ ] Slot filling: missing required inputs return a tool error; the agent asks
  the user and bound variables remember the answer. `bind` targets are
  validated against node and graph `initial_context`.
- [ ] Reply realization within node policy; `body` plus `intent` uses the body
  as anchor and fallback; policy checks before queueing.
- [ ] Deterministic fallback when no provider is configured or a call fails.
- [ ] Bounded agent loop: max tools per message and timeout from `llm.agent`.
- [ ] Async APIs: agent-driven polling within the tool budget (call, check
  status, retry); longer waits continue on later messages.
- [ ] Per-message logging of node, intent, tools called, provider, and policy
  version.
- [ ] Tests: provider fakes, classification cases, tool argument validation,
  policy rejection fallback, and evaluation fixtures (golden conversations).

Gate: the same graph runs with no LLM (deterministic) and with an LLM
(routing, realization, tool use, or a combination), with logs that explain
each decision.

### Phase 8 — CLI and REST API

Goal: operate a fleet without hand-editing config for daily operations.

Deliverables:

- [ ] CLI: `validate`, `auth`, `daemon`, `status`, `reload`.
- [ ] Config discovery: `--config`, then `~/.config/botforje/config.yml`, then
  `/etc/botforje/config.yml` (first match wins).
- [ ] `status` reports uptime, per-bot connection state, outbox depth, and
  active sessions; a `--json` mode serves scripts.
- [ ] Signals: SIGINT/SIGTERM graceful shutdown; SIGHUP reloads config.
- [ ] REST API: status, bots, sessions, conversations, messages, and config.
- [ ] Auth: single admin key, hashed at rest, regenerable; session cookie for
  the future UI.
- [ ] Config reload (ADR 0009): failed validation keeps the previous config;
  safe changes apply immediately; incompatible changes wait in a pending
  queue for maintenance mode.
- [ ] Tests: API handlers, auth, config discovery, and both reload classes.

Gate: a fresh instance can be validated, authenticated, started, inspected,
and reloaded through CLI and API only.

### Phase 9 — MVP hardening and release

Goal: an MVP someone can run from a clean machine.

Deliverables:

- [ ] Reference bot: a small support menu graph with intents and one `request`
  step.
- [ ] Operational notes: systemd unit, backup of SQLite and the device store,
  and account-safety practices (warmup for new accounts, no bulk outreach,
  human fallback when a bot is restricted) (ADR 0011).
- [ ] Log and error review: actionable messages, no secrets in logs.
- [ ] Documentation pass: README, scope, and architecture match the code.
- [ ] Release checklist and version tag.

Gate: a clean-machine run of the documented path works without tribal
knowledge.

## Deferred (post-MVP)

- Dashboard and web UI; once it can edit behavior, behavior moves to the
  database with YAML import/export (ADR 0002).
- Automatic graph extraction from chat history: opt-in only, limited to the
  history sync WhatsApp provides plus new conversations.
- Autonomous graph evolution: limits, validation, approval, deployment, and
  rollback, whether changes come from chat history or an admin prompt.
- SaaS infrastructure: login, tenant onboarding, scoped dashboards. Separate
  project built on BotForje; scope isolation is already in the model.
- Additional messaging providers (Telegram, SMS) and other WhatsApp channels
  (official Cloud API) behind `MessageChannel`.
- Script step: language, sandbox, permissions, timeouts, memory, network,
  and filesystem access are undecided.
- Scheduled maintenance windows for incompatible config changes.
- Metrics and tracing beyond structured logs.

## Open decisions

- Schema generator choice and approval (Phase 1; candidate:
  `invopop/jsonschema`).
- Fuzzy matching algorithm and defaults (close in Phase 4).
- Structured policy shape and conflict precedence (Phase 4; free string now).
- Tool result template syntax (close in Phase 4/7).
- Deterministic polling step shape for async APIs: interval, timeout,
  success condition (Phase 4/7).
- Context variable typing and empty-versus-default semantics (Phase 4).
- Confirmation guard for tools with side effects (Phase 7).
- Agent bounds defaults: max tools per message and timeout (Phase 7).
- Which LLM providers ship first (close in Phase 7).
- Whether the CLI acts as a client of the REST API for daemon operations
  (Phase 8).
- API shape and auth details (close in Phase 8).

Resolved: Go structs are the schema source and runtime validation happens in
Go (ADR 0007); the toolchain, CLI approach, and test layout are in ADR 0012.
The config v1 shape is frozen in Phase 1.

Candidate dependencies from the earlier plan (`gopkg.in/yaml.v3`) are
candidates, not commitments. Confirm in Phase 0/1. `urfave/cli/v3` and
`google/jsonschema-go` are no longer needed: the CLI uses the standard
library (ADR 0012) and runtime validation is in Go (ADR 0007).
