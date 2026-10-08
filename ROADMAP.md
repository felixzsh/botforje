# Botflota — Roadmap

What comes after the MVP and in what order. `PLAN.md` owns the phases that
lead to the MVP (0 to 9); this file owns Phase 10 onward. The MVP scope is in
`docs/scope.md`; settled decisions live in `docs/adr/`.

## How to read this roadmap

- Phase numbers continue `PLAN.md`: the MVP ends at Phase 9.
- Order follows dependencies and what an operator needs soonest, not what is
  most interesting to build.
- The MVP is the current work; nothing here starts before Phase 9 closes.
- Each phase has a goal, deliverables, and a gate. Deliverables are
  checkboxes; check one in the same commit that completes it.
- Only the MVP is a commitment. Phases here are a direction; a course change
  is recorded as an ADR, never as silent drift.
- Splitting a phase when it starts is expected. Reordering needs the reason
  written down.

## The line: MVP release (end of Phase 9)

When Phase 9 closes, a clean machine and one config file give:

- `config validate` over a settled config baseline, with a generated schema for
  editors.
- A fleet of WhatsApp accounts paired with `bot pair` and run with
  `fleet run`, answering from YAML graphs deterministically, with System One routing
  plus prescribed replies, or with an optional LLM.
- SQLite persistence for the device store, chats, sessions, and messages.
- CLI groups `config`, `bot`, `fleet`, and `message`, plus a REST API with owner
  and scoped tokens and no UI.
- One reference bot and operational notes for systemd, backup, and account
  safety.

No dashboard, no browser, and no hosting platform. That is the base every
phase below builds on.

## Settled direction

ADR 0046 owns the runtime/host boundary. Open design details become ADRs when
their owning phase starts; proposed policy is not a release commitment.

- One runtime, independent callers: an owner can access every scope; a scoped
  principal reaches only its granted scopes. A host can expose its own
  application and keep integration credentials server-side.
- Scope is the isolation boundary. Isolation is enforced on the server for
  every route, never in the UI.
- The API is the complete control plane for scope operations: every
  host- or user-initiated capability on scope-owned configuration, bots, and
  data is available through the API, authorized by owner or scope; both
  authorities read and write inside their reach. The dashboard and
  integrations use the same API; they never bypass it.
- One process may run bots across many scopes. A bot's lifecycle operation or
  ordinary runtime error must not stop its peers; the host's supervisor owns
  process lifecycle.
- The owner can operate on every scope and manage instance-level settings.
  Scoped authority covers reads and writes inside its authorized scopes;
  instance-wide settings and process lifecycle are outside it. A request to
  restart the process is owner-only and delegates to the host's supervisor.
- Botflota knows nothing about whoever hosts it: it knows trusted issuers,
  scoped credentials, and webhooks, and the host's account system lives outside
  this repository.
- How a host maps external identities to scopes and instances is its convention,
  not a rule Botflota enforces. Logical scope isolation is not a sandbox: the
  owner can bound files, HTTP egress, and a scope's secret names (ADRs
  0047–0049), while shared resource use remains the host's
  (`docs/integration.md`).
- A future operator dashboard is optional and embedded in the release binary.
  It is not a host application's required UI and does not gate integration.
- Behavior stays in YAML, including when the dashboard edits it: the API
  writes validated fragments and reloads them, so the UI is never a second
  source of truth (ADR 0002).
- A passkey or a browser session is never imported: the companion identity lives
  in the device store and runs on a server, while every passkey ceremony happens
  on the account owner's device (ADR 0028).
- Package layout: a product capability gets its own package; a subordinate
  detail lives inside its owner or, as a last resort, in `internal/`
  (ADR 0034; the map is in `docs/architecture.md`).
- AI stays optional and specialised (ADR 0030, ADR 0032, ADR 0033): System
  One (Jev) makes typed, confidence-bearing decisions for routing, policy, and
  tools, prescribed replies are the default, and a generative LLM only reads
  free text and realizes replies as an opt-in. Deterministic matching comes
  first.

## Phases

### Phase 10 — Live API and event stream

Goal: the API can feed a real-time dashboard with no polling, and every read a
dashboard needs already exists.

Deliverables:

- [ ] API review: `/api/v1` prefix, cursor pagination, filters, and one error
  shape (code, message, details) across handlers.
- [ ] It extends the REST API of Phase 7 (`PLAN.md`) rather than replacing it:
  the same routes gain pagination, filters, and the live stream.
- [ ] Bot identity is `(scope, bot)` everywhere it travels: parameters,
  events, audit entries, and logs never carry a bare bot name.
- [ ] Pairing sessions (Phase 7) stream their state changes like any other
  event, so a dashboard follows onboarding without polling.
- [ ] Reads a dashboard needs: bots with connection state, uptime, and outbox
  depth; sessions with visited nodes; chats with the last message; messages
  with pagination and search.
- [ ] Server-sent events: one stream per credential with typed events for
  inbound and outbound messages, session start and end, connection changes,
  and outbox depth.
- [ ] Bounded per-stream buffers and a documented slow-consumer policy that
  drops and resyncs instead of blocking a bot actor.
- [ ] The principal is resolved in one place, so Phase 13 only swaps the
  resolver; owner and scoped bearer tokens already exist.
- [ ] Tests: handlers, cursor pagination, stream ordering, and backpressure.

Gate: a script follows a live conversation and reconstructs chat, session, and
message state from the API and stream alone, with the fleet running.

### Phase 11 — Optional operator dashboard

Goal: an operator watches an instance from the browser without SSH or file
access. This is deferred tooling, not the UI for an independent host application.

Deliverables:

- [ ] ADR: UI stack. Constraint: the release binary embeds the dashboard and
  starts with no auxiliary process; decide between server-rendered pages and
  an embedded single-page app.
- [ ] Login with the owner token, browser session cookie, and logout.
- [ ] Fleet overview: per-bot connection state, uptime, outbox depth, active
  sessions, and last error. No limit-set catalog or exhaustion policy is assumed.
- [ ] Scope list and per-scope bot list, with scope identity visible in URLs
  and titles. Any scoped projection must enforce authority server-side.
- [ ] Live conversation view: inbound and outbound timeline per chat,
  streaming, with the session and its visited nodes beside it.
- [ ] Chat and message search over history, with the indexes those reads need
  (chats by last activity, messages by time) instead of scanning.
- [ ] Log tail over the structured log stream.
- [ ] Read-only by construction: no route added in this phase mutates state.
- [ ] Tests: page rendering, stream consumption, and one browser end-to-end
  check against a fake channel.

Gate: the operator starts a fleet and answers every operational question (is it
connected, what did it reply, why did it route there) from the browser alone.

### Phase 12 — Operator controls and manual takeover

Goal: a human steps into a conversation and steps out without leaving the
browser and without the bot talking over them.

Deliverables:

- [x] Per-chat control `auto`, `paused`, or `human`, live runtime state visible
  in the chat list; `settings.external_reply_pause` sets the automatic window,
  an external reply starts or renews it, and a restart returns every chat to
  `auto` by decision (ADR 0043).
- [ ] Manual reply travels through the bot's outbox, so pacing, jitter,
  presence, and read receipts stay unchanged.
- [x] While a chat is held, the engine records inbound messages and never
  replies; returning to `auto` resumes the session without replaying a
  backlog.
- [ ] Session actions: reset to root, force expire, and inspect the current
  visited nodes.
- [ ] Bot actions: reconnect, re-pair, unpair, start, stop, and restart, each
  with an audit entry and a confirmation step for destructive ones. A restart
  is also what forces a saved change that was deferred for live sessions,
  instead of waiting for those sessions to expire (ADR 0009).
- [ ] Conversation and bot controls are API operations addressed to their
  scope-owned resources; acting on one chat or bot never stops peer bots.
- [ ] Outbox view with queued and failed messages, retry, and cancel.
- [ ] Audit log: actor, action, scope, target, and time for every human
  action; messages written by a human are marked as such.
- [ ] Tests: takeover isolation, no double reply, outbox ordering, and audit
  completeness.

Gate: two operators take over two chats of the same bot, reply, and hand back,
with no bot reply in between and a complete audit trail.

### Phase 13 — Authority and scoped credentials

Goal: complete and test the generic authority model without requiring a
dashboard, delegated issuer, or external account model.

Deliverables:

- [ ] ADR: formalize the implemented owner/scoped authority model and its
  remaining lifecycle decisions. Every scope operation checks its principal.
- [ ] It replaces the resolver Phase 7 and Phase 10 introduced, not the route
  shapes those phases settled.
- [ ] TBD — grant replacement and rotation, including `exclusive_scopes`;
  separate these operations from aggregate resource policy.
- [x] Scoped access tokens: mint, list, revoke, hashed at rest, optional
  expiry, usable by scripts and by server-to-server callers. (Landed with the
  authorization base in Phase 7.) A scope grant is shareable between tokens
  by default and exclusive only where the instance sets `exclusive_scopes`,
  which also refuses startup on an overlap already stored (`docs/api.md`).
- [ ] Optional operator browser sessions: HttpOnly cookies, CSRF protection on
  mutations, and logout invalidation. Server-to-server integration needs none.
- [ ] Isolation guarantees, enforced as tests: a scoped principal cannot read,
  write, list, or infer the existence of another scope (out-of-scope targets
  answer 404, never 403), cannot read instance settings, logs, or backups, and
  cannot mint a credential wider than itself.
- [ ] Event streams and rate limits are per credential and filtered by
  authority on the server.
- [ ] Audit log per scope; instance-level entries only for instance authority.
- [ ] Tests: one authorization matrix over every route by authority, plus a
  stream leak test.

#### Optional delegated sign-in — TBD

Only design issuer trust and browser handoff if a real operator-interface
integration requires it. Token format, transport, replay prevention, issuer
grant ceilings, and key rotation need an ADR first. A host application can
authenticate its own callers and use existing scoped bearer tokens from its
backend; this path does not require delegated sign-in.

Gate: the API authorization matrix and stream isolation tests pass, and
credential lifecycle does not grant wider access than requested. Browser
tooling is a separate optional gate, not a requirement for host integration.

### Phase 14 — Independent host integration

Goal: an external application operates authorized resources through the API
without depending on Botflota's UI or writing its live files or database.

Deliverables:

- [x] Responsibility map and server-to-server flow in `docs/integration.md`
  (ADR 0046). The host owns its application and external identity mapping.
- [ ] TBD — minimal independent owner provisioning and grant operations,
  after their ADR; no required combined policy/scope/token operation.
- [ ] Instance identity, version, and build information where independent
  callers need to distinguish instances.
- [ ] Extend ADR 0045's signed durable delivery with independently useful bot
  lifecycle events. Usage facts require explicit counting semantics and are not
  an exact enforcement mechanism (`docs/access-policy.md`).
- [ ] Document implemented payloads, lifecycle, recovery, and versioning in
  `docs/integration.md` and `docs/api.md`, without describing a particular host.
- [ ] Integration tests: a backend retains credentials, publishes an existing
  authorized scope, drives pairing, reads conversations, and verifies a webhook.
  Out-of-scope operations fail without affecting peer bots.

Gate: an independent client proves that flow without a dashboard or issuer.
New scope creation adds a gate only after its separate contract is accepted.
Authors a host does not fully trust need the owner controls in ADRs 0047–0049
and the host rules in `docs/integration.md`.

### Phase 15 — Authoring integration primitives

Goal: callers can author behavior independently while Botflota retains one
validator, YAML source of truth, and activation path (ADR 0002, ADR 0046).

Deliverables:

- [ ] Consider draft validation and a deterministic simulation capability only
  when a real caller needs them. Settle the API and side-effect boundary first;
  simulation must not silently run external requests or channel sends.
- [ ] Revisit scope/bot membership activation so a scoped change can update
  affected actors without restarting peers (ADR 0009).
- [ ] Consider whole-scope YAML import/export as runtime data operations, with
  reference validation and explicit treatment of shared model profiles.
- [ ] Tests for each accepted primitive: invalid definitions are rejected,
  simulations isolate effects, and activation preserves unrelated scopes.

The existing publication API is reused, not replaced. Visual editors, template
journeys, and draft storage belong to the caller's application, not this phase.
Gate: an independent authoring client can validate and publish through accepted
runtime primitives without an embedded editor.

### Phase 16 — Runtime history and retention

Goal: independent applications can use authorized history without owning the
runtime's stores. Botflota does not build an onboarding assistant.

Deliverables:

- [ ] TBD — retention defaults and authority, purge, and scope export, with
  explicit privacy and deduplication/outbox consequences.
- [ ] TBD — storage exhaustion and whether any retention policy can safely
  release capacity (`docs/access-policy.md`). No current aggregate pause policy
  is implied.
- [ ] Document channel history capabilities and limitations; account import and
  per-chat sync already exist, while Cloud API offers no history sync.
- [ ] Tests for accepted retention and export operations, including authority
  and consistency of runtime records.

Applications may analyze authorized history and propose drafts outside Botflota.
They own consent, provider disclosure, review, and the assisted setup experience.
Gate: accepted history operations preserve isolation and explicit data lifecycle.

### Phase 17 — Reserved; application-level evolution is outside the boundary

Proposal generation, outcome scoring, approval workflows, and autonomous
authoring are not Botflota commitments (ADR 0046). Independent applications may
use authorized reads and version-checked publication for those workflows.
Any requested evaluation or canary execution primitive needs its own runtime
case and ADR; no such mechanism is approved by reserving this phase number.

### Phase 18 — Reach and scale

Goal: more channels and more scopes than one process should carry.

Deliverables:

- [ ] WhatsApp Cloud API channel (`adapters/whatsappcloud`) behind
  `MessageChannel`, for accounts that prefer the official path and for
  accounts the unofficial path cannot serve (ADR 0028); onboarding and
  pricing differ, the graph does not.
- [ ] Telegram channel (`adapters/telegram`), the cheapest proof that the
  port is provider agnostic and the dashboard is not WhatsApp-specific.
- [ ] Instance sharding: several instances, one identity each, and documented
  guidance for spreading scopes across instances; aggregation **across
  instances** lives outside Botflota.
- [ ] Document that the host owns cross-instance coordination and policy.
  No limit-set implementation or aggregate enforcement model is assumed.
- [ ] Each channel maps its own settings from `config.BotSettings` (the CLI
  translates them for WhatsApp today), so a second provider adds no switch in
  `cmd/`.
- [ ] Data operations: backup and restore per scope, export of a scope into
  another instance, and a delete-scope operation that removes its rows and its
  device store.
- [ ] Metrics and tracing for operators, beyond structured logs.
- [ ] Tests: a second channel against the same fixture graph and scope
  deletion; verify no cross-instance limit is implied by sharding guidance.

Gate: the same graph runs on two providers, a scope moves between instances
through export and import, and deleting a scope leaves no residue.

### Phase 19 — Script step

Goal: local logic for steps that an HTTP endpoint should not carry.

Deliverables:

- [ ] ADR: language, sandbox, and permissions (network, filesystem, memory,
  timeouts) for the script step.
- [ ] Script step in the engine with the same inputs, guards, and result
  capture as `request`, and the same template rules.
- [ ] If a simulation primitive is accepted, support testing scripts without
  external side effects; application editor support stays outside this project.
- [ ] Tests: sandbox escapes, timeout, and resource limits.

Gate: a graph runs a script step deterministically, and the sandbox denies
network and filesystem access unless the ADR allows them.

This phase is independent and may be pulled earlier when users ask for logic
beyond an HTTP request. A script capability still needs its own safety decision.

## Cross-cutting themes

### Isolation

- Scope is the logical namespace and authorization boundary. Scope-owned queries
  and events carry it; owner authority covers the instance. An owner can bound
  what a definition reaches: local files with a file root (ADR 0047), outbound
  HTTP with an egress policy (ADR 0048), and a scope's secret names with a
  secret catalog (ADR 0049). Shared CPU, memory, and fairness stay the host's.
- With none of those set, a scope reads any file the process can, reaches any
  host the server can, and resolves any process variable; a host that shares an
  instance sets them per scope (ADR 0046).
- Nothing leaks by inference: out-of-scope targets answer 404, lists never
  include foreign names or counts, and errors and logs never name another
  scope.
- WhatsApp isolation is structural already: one number belongs to one bot of
  one scope, and references never cross scopes.

### Data, privacy, and retention

- WhatsApp content is third-party personal data. Retention defaults and authority
  are TBD; no automatic purge policy is implemented by this roadmap.
- Export and delete are first-class operations, not SQL handed to the host.
- Turn and outbox consistency remain governed by ADR 0040. Proposed content
  ceilings, exhaustion behavior, and retention are TBD (`docs/access-policy.md`);
  no implemented aggregate pause policy is implied.
- Whether instance authority reads message bodies of every scope, or only
  metadata, is an open decision below.

### Optional embedded operator UI

- If implemented, Botflota's operator dashboard uses embedded assets, with no
  sidecar or Node runtime in production. Hosts can build independent interfaces.
- A frontend toolchain, if any, is a build-time dependency only.

### API contract and compatibility

- `/api/v1` with additive changes only inside a version; deprecations
  announced one minor version ahead with a date. The caller pins a version.
- The migration runner stays append-only (ADR 0006): once a release ships,
  migrations are forever.

### Licensing and branding

- AGPL-3.0-or-later, decided (ADR 0050). Commercial hosting is allowed subject
  to the license; `LICENSE` contains the terms and `README.md` the project notice.
- Keep a hosted product brand-independent: no endorsement implied, and no
  Botflota name in the host's product.
- Contributions need no CLA. Future license changes require the necessary rights
  from copyright holders; dependency licenses remain independent (ADR 0050).

### Topology

- One instance can carry many scopes; sharding is an operations decision, not
  a code branch. Group scopes by volume and risk; how a host maps its accounts
  to instances is its own concern.

## Ordering and dependencies

- 10 and the API controls of 12 serve independent callers before UI work.
- 11 is optional tooling; it does not gate 12, 13, or 14's server-side work.
- Existing scoped tokens and publication already permit integration work;
  remaining authority and provisioning operations require their own gates.
- Onboarding authors who are not fully trusted requires a scope file root
  (ADR 0047) and the host rules in `docs/integration.md`, whatever interface
  they use.
- 15 and 16 supply only accepted runtime primitives, not a host application.
- 17 carries no implementation commitment.
- 18 can proceed when the required runtime contracts are stable; the Cloud API
  can be pulled earlier if account safety becomes the limiting factor.
- 19 is independent; pull it when a graph needs logic beyond an HTTP request.

## Open decisions

- TBD — priority and UI stack for the optional operator dashboard (Phase 11).
- Whether instance authority sees message bodies of every scope by default,
  or metadata only with explicit opt-in (Phase 11/13).
- TBD — whether delegated sign-in is needed at all, and its contract if so
  (Phase 13). Backend integration uses existing bearer tokens.
- Whether `shared` may ever carry `actions` or `graphs`, and the governance
  that would need (Phase 15).
- How export and import inline a scope's reference to `shared` (Phase 18).
- Retention defaults per scope and who may change them (Phase 16).
- TBD — minimal scope provisioning, grant replacement, naming, and recovery
  (Phase 7; `docs/access-policy.md`).
- TBD — operational sharding guidance (Phase 18). External identity-to-instance
  mapping belongs to the host (ADR 0046).
- TBD — aggregate policy ownership, exact enforcement, periods, durable usage,
  and owner-policy recovery (Phase 7; `docs/access-policy.md`).
- TBD — shared resource use between scopes: CPU, memory, and fairness
  (`docs/integration.md`). Files, HTTP egress, and secret names are settled by
  ADRs 0047–0049.
- TBD — optional implicit-scope syntax (`PLAN.md`).

## Outside the boundary

ADR 0046 owns this boundary; `docs/integration.md` describes the flow. A host
owns its application, external identity model, guided authoring, assisted
onboarding, and application-level evolution. It maps its own identities to
scopes and instances and interprets runtime facts for its own policies.

The API is the integration seam. A host backend can retain scoped credentials
and consume API reads, publication, pairing, and signed events without an
issuer, browser handoff, or embedded dashboard. Provisioning and aggregate
policy remain TBD, not prerequisites bundled into that flow.

Providing the model is the same boundary: a host-run gateway is just an `llm`
entry with a `base_url` and a key, so Botflota needs no new concept to route
model traffic through a gateway.

## Explicitly not planned

- External identity registries, host branding, and host-specific offering rules.
- A required host dashboard, visual authoring application, onboarding assistant,
  or autonomous proposal engine inside Botflota.
- Browser automation for WhatsApp.
- Behavior tables in SQLite: YAML stays the behavior source of truth and the
  dashboard edits it through the API (ADR 0002).
- References or graphs shared between scopes.
- A second database engine for the hosted path; SQLite is enough until a
  real limit appears.
