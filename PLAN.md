# BotForje — Plan

Phase-by-phase plan to the MVP. This file replaces the earlier sprint plan
and the legacy `CURRENT_PLAN.md` snapshot. Settled decisions live in
`docs/adr/`; open decisions are listed at the end.

## How to read this plan

- One phase at a time, in order. Do not start a phase before its
  dependencies are done.
- Every phase ends with a gate: tests that prove the phase's behavior. A
  phase is not done until its gate passes.
- The plan is not a contract. Adjust it when real needs appear, and record
  course changes as ADRs.
- No dashboard, no SaaS, and no behavior in the database during the MVP.

## MVP

A config-driven fleet runner roughly equivalent to `botforje-js` in
capability: multiple WhatsApp bots from one process, YAML-defined behavior,
SQLite-backed sessions, hybrid LLM support, CLI, and REST API. No dashboard.

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

- Documentation set: `README.md`, `CONTEXT.md`, `PLAN.md`,
  `docs/architecture.md`, `docs/adr/`, `AGENTS.md`.
- Toolchain decisions recorded: Go version, CLI approach, test layout.
- Time-boxed spike: pair a WhatsApp account with whatsmeow, persist the
  device store, receive and send one message. Throwaway code, not merged.
- Test conventions: table-driven tests and fixture layout.

Gate: the spike proves pairing and messaging work; conventions are written.

Out of scope: any product code.

### Phase 1 — Config engine, config v1, and `validate`

Goal: freeze the config v1 shape and turn YAML into a validated typed model
with precise errors.

Deliverables:

- Design task first: freeze config v1 (node `goal`/`policy`/`tools`,
  action `inputs`, edge `intent`/`triggers`, message modes, scope, `llm`
  section). Align `config.example.yaml` and the draft `config.schema.json`
  with it.
- Domain types: Action, Graph, Node, Edge, Step, Bot, Settings, Guard, LLM.
- Loader: main config plus `include` fragments (file, glob, directory),
  merge, and scope handling.
- Runtime validation in Go: strict YAML decoding plus semantic checks.
  The JSON Schema is generated from the Go types for editors only (ADR 0007).
- Semantic validation: reference resolution, scope isolation, uniqueness by
  (type, scope, name), node reachability, and message mode rules.
- Schema generation: pick and approve a generator (candidate:
  `invopop/jsonschema`) and wire the regeneration step.
- CLI command `validate` with readable output and a `--json` mode.
- Fixtures: valid and invalid cases for every rule.

Gate: `validate` accepts valid fixtures and rejects every invalid fixture
with an error naming file, entity, and reason. Unit tests for merge and
semantic validation; integration tests over fixtures.

### Phase 2 — Templates and context

Goal: `{{...}}` values resolve correctly in messages and requests.

Deliverables:

- Syntax and resolver for `{{path.to.value}}`.
- Contexts: graph, node, sender, message, bot, and request results.
- Read/write versus read-only context rules; node context resets on re-entry.
- Tests: resolution, missing paths, escaping, and request result capture.

Gate: message steps render correctly from a simulated context.

### Phase 3 — Persistence (SQLite)

Goal: runtime state survives restarts.

Deliverables:

- SQLite setup with WAL mode and migrations from the first table.
- Stores: whatsmeow device store, sessions, conversations, messages.
- Repository interfaces owned by the domain; SQL stays in the adapter.
- Tests: migrations, store round-trips, and session resume after restart.

Gate: a session persisted by one process resumes in a new process.

### Phase 4 — Graph engine

Goal: conversations run fully in memory, testable with no channel.

Deliverables:

- Session lifecycle: create at root, resume, and expire by timeout.
- Routing: deterministic text match first; `LLMProvider` port defined with a
  null implementation.
- Transitions: exit actions, `goto`, entry actions, and fallback node.
- Steps: `message`, `location`, and `request` with retry and result capture.
- Guards: cooldown per sender per action; blocked-action behavior.
- Hybrid intent semantics: classification fallback, rendering modes with
  anchor plus fallback, and policy checks (shape frozen in Phase 1;
  ADR 0003).
- Tests: simulated conversations including fallback, timeout, guards, and
  request failures.

Gate: a fixture graph produces the expected message sequence for a scripted
conversation with no channel and no LLM.

### Phase 5 — WhatsApp channel (whatsmeow)

Goal: one real bot talks to WhatsApp end to end.

Deliverables:

- `MessageChannel` port and the whatsmeow adapter behind it.
- QR pairing, device store persistence, and reconnect with state reporting.
- Inbound event mapping to `IncomingMessage` plus deduplication.
- Outbound send: text, location, and read receipts.
- CLI `auth <bot>` and `status`.
- Tests: adapter unit tests with fakes; a manual end-to-end checklist.

Gate: one configured bot answers a real WhatsApp message and survives a
restart without re-pairing. The rest of the system still runs without
whatsmeow; tests use a fake channel.

### Phase 6 — Fleet, inbox, outbox, daemon

Goal: multiple bots in one process, paced and isolated.

Deliverables:

- Fleet supervisor that builds one actor per bot from config.
- Inbox: routing, dedup, and filters (ignore groups, allow/ignore senders).
- Outbox: per-bot FIFO with `queue_delay` and backpressure.
- Daemon command: start all bots, graceful shutdown on SIGINT/SIGTERM, and
  status reporting.
- Tests: two bots with independent sessions and queues; filter behavior;
  shutdown drains queues.

Gate: two or more bot accounts run concurrently; messages from each are
answered with the configured pacing.

### Phase 7 — LLM providers, tools, and hybrid behavior

Goal: the LLM-first design becomes usable without making LLMs mandatory.

Deliverables:

- `LLMProvider` implementations: at least one cloud provider and one local
  option, selected by config.
- Intent classification as fallback for unmatched input.
- Agentic tool calling (ADR 0008): the agent invokes node `tools` with
  arguments validated against their `inputs`, chains calls, and stays in or
  leaves the node through declared edges only.
- Reply realization within node policy; `body` plus `intent` uses the body
  as anchor and fallback; policy checks before queueing.
- Deterministic fallback when no provider is configured or a call fails.
- Bounded agent loop: max tools per message and timeout from `llm.agent`.
- Per-message logging of node, intent, tools called, provider, and policy
  version.
- Tests: provider fakes, classification cases, tool argument validation,
  policy rejection fallback, and evaluation fixtures (golden conversations).

Gate: the same graph runs with no LLM (deterministic) and with an LLM
(routing, realization, tool use, or a combination), with logs that explain
each decision.

### Phase 8 — CLI and REST API

Goal: operate a fleet without hand-editing config for daily operations.

Deliverables:

- CLI: `validate`, `auth`, `daemon`, `status`, `reload`.
- REST API: status, bots, sessions, conversations, messages, and config.
- Auth: single admin key, hashed at rest, regenerable; session cookie for
  the future UI.
- Config reload: revalidate and apply safe changes; defer incompatible
  changes to a maintenance mode.
- Tests: API handlers, auth, and reload behavior.

Gate: a fresh instance can be validated, authenticated, started, inspected,
and reloaded through CLI and API only.

### Phase 9 — MVP hardening and release

Goal: an MVP someone can run from a clean machine.

Deliverables:

- Reference bot: a small support menu graph with intents and one `request`
  step.
- Operational notes: systemd unit, backup of SQLite and the device store.
- Log and error review: actionable messages, no secrets in logs.
- Documentation pass: README, CONTEXT, and architecture match the code.
- Release checklist and version tag.

Gate: a clean-machine run of the documented path works without tribal
knowledge.

## Deferred (post-MVP)

- Dashboard and web UI; once it can edit behavior, behavior moves to the
  database with YAML import/export (ADR 0002).
- Automatic graph extraction from chat history: opt-in only, limited to the
  history sync WhatsApp provides plus new conversations.
- SaaS infrastructure: login, tenant onboarding, scoped dashboards. Separate
  project built on BotForje; scope isolation is already in the model.
- Additional channels (Telegram, SMS) behind `MessageChannel`.
- Script step and sandboxing.
- Scheduled maintenance mode for incompatible config changes.
- Metrics and tracing beyond structured logs.

## Open decisions

- CLI approach: standard library flags versus a CLI library (Phase 0/1).
- Schema generator choice and approval (Phase 1; candidate:
  `invopop/jsonschema`).
- Fuzzy matching algorithm and defaults (close in Phase 4).
- Structured policy shape and conflict precedence (Phase 4; free string now).
- Tool result template syntax (close in Phase 4/7).
- Confirmation guard for tools with side effects (Phase 7).
- Agent bounds defaults: max tools per message and timeout (Phase 7).
- Which LLM providers ship first (close in Phase 7).
- API shape and auth details (close in Phase 8).

Resolved: Go structs are the schema source and runtime validation happens in
Go (ADR 0007). The config v1 shape is frozen in Phase 1.

Candidate dependencies from the earlier plan (`gopkg.in/yaml.v3`,
`urfave/cli/v3`) are candidates, not commitments. Confirm each in Phase 0/1.
`google/jsonschema-go` is no longer needed: runtime validation is in Go
(ADR 0007).
