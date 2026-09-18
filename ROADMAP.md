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

- `config validate` over a frozen config v1, with a generated schema for
  editors.
- A fleet of WhatsApp accounts paired with `bot pair` and run with
  `fleet run`, answering from YAML graphs, deterministically, with decision
  routing, or with an LLM.
- SQLite persistence for the device store, chats, sessions, and messages.
- CLI groups `config`, `bot`, `fleet`, and `message`, plus a REST API with a
  single admin key and no UI.
- One reference bot and operational notes for systemd, backup, and account
  safety.

No dashboard, no tenants, no browser. That is the base every phase below
builds on.

## Settled direction

These shape every phase and become ADRs when the phase that owns them starts.

- One binary, two audiences: an instance view (every scope) and a scoped view
  (one scope) that cannot prove another scope exists.
- Scope is the isolation boundary and the only place a tenant appears.
  Isolation is enforced on the server for every route, never in the UI.
- Botflota knows nothing about whoever hosts it. It knows trusted issuers,
  scoped credentials, and webhooks; accounts, signup, and account management
  live outside this repository.
- One scope per tenant is a convention a host may follow, not a rule Botflota
  enforces.
- The dashboard ships inside the release binary: no auxiliary process and no
  Node runtime in production.
- Behavior stays in YAML until Phase 15 moves it to the database. Until then
  the UI is a client of the API, never a second source of truth.
- A passkey or a browser session is never imported: the companion identity lives
  in the device store and runs on a server, while every passkey ceremony happens
  on the account owner's device (ADR 0028).
- AI stays optional and specialised (ADR 0030): a decision provider (Jev) makes
  typed, confidence-bearing decisions for routing and policy, while a generative
  LLM only realizes replies and calls tools. Deterministic matching comes first.

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
  resolver; authentication stays the admin key for now.
- [ ] Tests: handlers, cursor pagination, stream ordering, and backpressure.

Gate: a script follows a live conversation and reconstructs chat, session, and
message state from the API and stream alone, with the fleet running.

### Phase 11 — Dashboard, instance view

Goal: the host operator watches the whole instance from the browser, with no
SSH and no file access.

Deliverables:

- [ ] ADR: UI stack. Constraint: the release binary embeds the dashboard and
  starts with no auxiliary process; decide between server-rendered pages and
  an embedded single-page app.
- [ ] Login with the admin key, browser session cookie, and logout.
- [ ] Fleet overview: per-bot connection state, uptime, outbox depth, active
  sessions, and last error.
- [ ] Scope list and per-scope bot list. The scope is part of every URL and
  page title, so the scoped view of Phase 13 is the same pages with one scope.
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

- [ ] Chat control state `auto` or `human`, persisted per chat and visible in
  the chat list.
- [ ] Manual reply travels through the bot's outbox, so pacing, jitter,
  presence, and read receipts stay unchanged.
- [ ] While a chat is `human`, the engine records inbound messages and never
  replies; returning to `auto` resumes the session without replaying a
  backlog.
- [ ] Session actions: reset to root, force expire, and inspect the current
  visited nodes.
- [ ] Bot actions: reconnect, re-pair, unpair, and stop, each with an audit
  entry and a confirmation step for destructive ones.
- [ ] Outbox view with queued and failed messages, retry, and cancel.
- [ ] Audit log: actor, action, scope, target, and time for every human
  action; messages written by a human are marked as such.
- [ ] Tests: takeover isolation, no double reply, outbox ordering, and audit
  completeness.

Gate: two operators take over two chats of the same bot, reply, and hand back,
with no bot reply in between and a complete audit trail.

### Phase 13 — Authority and scoped credentials

Goal: one instance natively serves two audiences — the host, who sees
everything, and a scoped principal, who sees exactly one scope and cannot
prove another exists.

Deliverables:

- [ ] ADR: authority model. A credential yields a principal with an authority
  (`instance` or one scope), a role, and an expiry. Every route resolves a
  target scope, and authorization is one check against that principal.
- [ ] It replaces the resolver Phase 7 and Phase 10 introduced, not the route
  shapes those phases settled.
- [ ] ADR: delegated sign-in (see below).
- [ ] Roles, kept small: `viewer` reads, `operator` also controls
  conversations, `admin` also manages configuration and tokens inside the
  scope. Instance authority additionally has `owner`.
- [ ] Scoped API keys: mint, list, revoke, hashed at rest, optional expiry,
  usable by scripts and by server-to-server callers.
- [ ] Browser sessions: scope-limited, HttpOnly cookie, CSRF token on mutating
  requests, logout invalidates the session.
- [ ] Isolation guarantees, enforced as tests: a scoped principal cannot read,
  write, list, or infer the existence of another scope (out-of-scope targets
  answer 404, never 403), cannot read instance settings, logs, or backups, and
  cannot mint a credential wider than itself.
- [ ] Event streams and rate limits are per credential and filtered by
  authority on the server.
- [ ] Audit log per scope; instance-level entries only for instance authority.
- [ ] Tests: one authorization matrix over every route by role and authority,
  plus a stream leak test.

#### Delegated sign-in (recommended shape)

- Botflota trusts issuers, never a particular product. Configuration lists
  each issuer with its key source (JWKS URL or static public key), audience,
  clock skew, and claim mapping (default: a `scope` claim, or a `scopes`
  list).
- The issuer signs a short-lived token with subject, audience, scope, role,
  and expiry; a role may not exceed what the issuer is allowed to grant.
- The browser lands on Botflota with the token, Botflota verifies the
  signature offline, rejects reuse, sets the session cookie, and redirects to
  the dashboard. The token is never stored server side and never stays in the
  address bar.
- A host with no issuer mints scoped API keys with the admin key. The
  fallback of one-time login links minted by the API stays in reserve.
- Rationale: no callback, no shared session state, no vendor vocabulary, and a
  token minted for one instance is useless on another.

Gate: the same binary serves an instance dashboard that sees every scope and a
scope dashboard that sees one, and the isolation test suite passes with no
route exempt.

### Phase 14 — Multi-tenant surface and host integration

Goal: a tenant user lands in Botflota and sees only their scope, while the
system that authenticated them stays something Botflota knows nothing about.

Deliverables:

- [ ] Scope projection of the dashboard: the same pages with no scope switcher
  and no instance settings, and a header that shows the scope name.
- [ ] Scope provisioning: apply a scope config document to the running
  instance (validate, write one include fragment per scope, reload, keep the
  previous state on any error), so a tenant is created without hand-editing
  the main file. Reuses include fragments (ADR 0016) and reload classes
  (ADR 0009).
- [ ] Instance identity: `instance_id`, version, and build info in responses
  and events, so a caller with several instances can tell them apart.
- [ ] Signed outbound webhooks with retries and backoff: bot connected,
  disconnected, logged out, or banned; bot created or removed; per-scope
  message counters and connected bot time for usage reports; threshold
  warnings.
- [ ] `docs/integration/scoped-hosting.md`: the Botflota side of the contract
  only (issuer configuration, token claims, webhook payloads and signatures,
  scope naming, API versioning and deprecation). The other side is out of
  this repository.
- [ ] Tests: a stub issuer signs a token, a browser lands in the scoped
  dashboard, and webhooks are delivered with retry and signature checks.

Gate: an external harness acting as an issuer creates a scope, signs a
sign-in token, lands a browser, sees only that scope, and receives usage
webhooks, with no code for any particular host inside Botflota.

### Phase 15 — Behavior in the database and the editor

Goal: everything YAML expresses today can be edited in the dashboard, with
validation, review, and rollback.

Deliverables:

- [ ] ADR: supersedes ADR 0002. Behavior tables in SQLite, YAML as import and
  export, one-time migration from the config directory.
- [ ] Versioned store: every change is a revision with author, time, and diff;
  publish is atomic; rollback is one action.
- [ ] Validation before publish: the same semantic rules as `config validate`
  plus template reference checks; a rejected publish changes nothing.
- [ ] Publish semantics reuse the reload classes (ADR 0009): safe changes
  apply live, incompatible ones wait for a window.
- [ ] Editors for bots, graphs, nodes, edges, actions, steps, guards, llms,
  and scope settings, with a YAML view beside the form.
- [ ] Graph editor: topology view with node and edge editing, and
  reachability and reference errors shown in place.
- [ ] Simulator: run a scripted conversation against the draft graph on the
  fake channel, with no WhatsApp account; save the script as a case.
- [ ] Starter templates: packaged graphs (support menu, appointment request,
  quote) a scope can instantiate and edit.
- [ ] Import and export of a whole scope as YAML, with the publish validation.
- [ ] Decide where behavior types live once the database is their source:
  `internal/config` owns `Graph`, `Action`, and `Node` today and the engine
  imports them. Split loading from the behavior model when the store lands,
  not before.
- [ ] Tests: migration from a fixture config directory, concurrent edit
  conflict, publish and rollback, simulator cases.

Gate: an operator with no file access creates a bot from a template, edits its
graph, simulates a conversation, publishes it, sees it live, and rolls it back.
This phase may split into the store and the editors; the gate applies to both.

### Phase 16 — Onboarding assistant and history analysis

Goal: a newly paired WhatsApp account proposes a draft graph built from its
own conversations, for a human to review.

Deliverables:

- [ ] Opt-in history ingestion: request and store the history WhatsApp offers
  for a newly paired account, in the scope's own tables; off by default.
  Accounts served through the Cloud API are out of this phase: that channel
  exposes no history sync (ADR 0028).
- [ ] Retention and privacy: per-scope retention window, purge, and export;
  analysis runs on stored history and clearly states what leaves the instance
  when the configured provider requires it.
- [ ] Analysis: cluster conversations, infer intents and slots, and propose
  nodes, edges, and actions, each with the messages that support it.
- [ ] Draft output: a scope config document that passes `config validate`. A
  suggested step whose endpoint or script does not exist becomes an explicit
  stub, flagged in validation and listed as a checklist item.
- [ ] Review flow in the dashboard: accept or reject each suggestion, then
  publish through Phase 15.
- [ ] Optional shared llm catalog: an instance-level provider a scope may
  reference, for hosts that provide the model themselves; BYOK stays
  (supersedes part of ADR 0017).
- [ ] Tests: fixture transcripts to a deterministic draft shape, stub
  detection, and a publish of the generated draft.

Gate: a freshly paired account yields a reviewed, validated graph that answers
the most common intents in its own history, with unimplemented steps listed
instead of silently invented.

### Phase 17 — Autonomous evolution

Goal: graphs improve from live traffic under evaluation, approval, and
rollback, never on their own authority.

Deliverables:

- [ ] Proposal engine: from live conversations, outcomes, and unresolved
  intents, propose edits to nodes, edges, and actions.
- [ ] Evaluation harness: golden conversations per graph and a score
  (resolution rate, handover rate, policy violations, cost); a proposal with
  no score is not eligible.
- [ ] Approval policy per scope: always manual by default, automatic only
  inside declared bounds.
- [ ] Canary: apply a proposal to a share of chats, watch the score, then
  promote or revert automatically.
- [ ] Every applied proposal is a revision that reverts in one action.
- [ ] Budgets: per-scope token and request budgets that bound the loop.
- [ ] Tests: regression detection, canary revert, budget enforcement, and
  audit completeness.

Gate: a proposal that lowers the score is reverted by the canary with no human
involved and one that raises it waits for approval, both visible in the audit
log.

### Phase 18 — Reach and scale

Goal: more channels and more tenants than one process should carry.

Deliverables:

- [ ] WhatsApp Cloud API channel behind `MessageChannel`, for accounts that
  prefer the official path and for accounts the unofficial path cannot serve
  (ADR 0028); onboarding and pricing differ, the graph does not.
- [ ] Telegram channel, the cheapest proof that the port is provider agnostic
  and the dashboard is not WhatsApp-specific.
- [ ] Instance sharding: several instances, one identity each, and documented
  guidance for mapping a tenant to an instance; aggregation lives outside
  Botflota.
- [ ] Per-scope quotas: bot count, message rate, and storage, enforced with
  clear errors; the host sets them, Botflota enforces them.
- [ ] Each channel maps its own settings from `config.BotSettings` (the CLI
  translates them for WhatsApp today), so a second provider adds no switch in
  `cmd/`.
- [ ] Data operations: backup and restore per scope, export of a scope into
  another instance, and a delete-scope operation that removes its rows and its
  device store.
- [ ] Metrics and tracing for operators, beyond structured logs.
- [ ] Tests: a second channel against the same fixture graph, quota
  enforcement, and scope deletion.

Gate: the same graph runs on two providers, a scope moves between instances
through export and import, and deleting a scope leaves no residue.

### Phase 19 — Script step

Goal: local logic for steps that an HTTP endpoint should not carry.

Deliverables:

- [ ] ADR: language, sandbox, and permissions (network, filesystem, memory,
  timeouts) for the script step.
- [ ] Script step in the engine with the same inputs, guards, and result
  capture as `request`, and the same template rules.
- [ ] Editor support in the simulator so a script can be tried before publish.
- [ ] Tests: sandbox escapes, timeout, and resource limits.

Gate: a graph runs a script step deterministically, and the sandbox denies
network and filesystem access unless the ADR allows them.

This phase is independent and may be pulled earlier when users ask for logic
beyond an HTTP request. It also completes the stubs Phase 16 can generate.

## Cross-cutting themes

### Isolation

- Scope is the only isolation boundary. Every query and every event is scoped;
  instance-level reads are the only cross-scope reads and they are read-only
  aggregations.
- Nothing leaks by inference: out-of-scope targets answer 404, lists never
  include foreign names or counts, and errors and logs never name another
  scope.
- WhatsApp isolation is structural already: one number belongs to one bot of
  one scope, and references never cross scopes.

### Data, privacy, and retention

- WhatsApp content is third-party personal data. Retention is per scope and
  defaults to what operations need, not to forever.
- Export and delete are first-class operations, not SQL handed to the host.
- Whether instance authority reads message bodies of every scope, or only
  metadata, is an open decision below.

### One binary, embedded UI

- The release binary serves the dashboard from embedded assets; no sidecar and
  no Node runtime in production.
- A frontend toolchain, if any, is a build-time dependency only.

### API contract and compatibility

- `/api/v1` with additive changes only inside a version; deprecations
  announced one minor version ahead with a date. The caller pins a version.
- The migration runner stays append-only (ADR 0006): once a tenant exists,
  migrations are forever.

### Licensing and branding

- MIT, decided. Anyone may host Botflota commercially; that is the cost of
  adoption, and the value of a hosted service is operation, not the source.
- Keep a hosted product brand-independent: no endorsement implied, and no
  Botflota name in a tenant-facing product.
- Contributions need no CLA, so relicensing later would require every
  copyright holder; decide that now, not after the first outside contribution.

### Topology

- One instance can carry many scopes; sharding is an operations decision, not
  a code branch. Group tenants by volume and risk, and keep the
  tenant-to-instance mapping outside Botflota.

## Ordering and dependencies

- 10 → 11 → 12 is the shortest path to running a fleet without a terminal:
  watch and take over.
- 13 before 14, authority before tenant surface, never the reverse.
- 14 before onboarding real tenants: provisioning, sign-in, and webhooks come
  first.
- 15 before 16: the assistant's output must be reviewable and publishable.
- 17 last: it needs 15 (revisions), 16 (analysis), and a base of real
  conversations to evaluate against.
- 18 runs in parallel once 13 and 14 are stable; the Cloud API can be pulled
  earlier if account safety becomes the limiting factor.
- 19 is independent; pull it when a graph needs logic beyond an HTTP request.

## Open decisions

- UI stack for the embedded dashboard (Phase 11).
- Whether instance authority sees message bodies of every scope by default,
  or metadata only with explicit opt-in (Phase 11/13).
- Token transport for delegated sign-in: landing page with a fragment and a
  one-time exchange, or a code exchange (Phase 13).
- Whether the shared llm catalog supersedes the per-scope only rule of
  ADR 0017 (Phase 16).
- Retention defaults per scope and who may change them (Phase 16).
- Scope provisioning: where the writable config directory lives and whether a
  previous version is kept per applied document (Phase 14).
- Sharding threshold, and whether the host or Botflota maps a tenant to an
  instance (Phase 18).
- Quota dimensions and the enforcement point (Phase 18).

## Outside the boundary

Recorded here only to keep the boundary visible. Botflota is the runner, not
the account system: accounts, signup, plans, and tenant-facing branding belong
to whatever hosts it. Such a system reaches a Botflota instance through the
Phase 13 and Phase 14 surface and nothing else: it configures an issuer, signs
short-lived scoped tokens, listens to webhooks for usage, and provisions
scopes.

Providing the model is the same boundary: a host-run gateway is just an `llm`
entry with a `base_url` and a key, so Botflota needs no new concept to route
model traffic through a gateway.

## Explicitly not planned

- Account-system features inside Botflota: user registry, signup, plans, or
  tenant-facing branding.
- Browser automation for WhatsApp.
- Behavior in the database before Phase 15.
- References or graphs shared between scopes.
- A second database engine for the multi-tenant path; SQLite is enough until a
  real limit appears.
