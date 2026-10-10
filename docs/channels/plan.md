# Messaging channel preparation plan

Status: draft, not approved for implementation. P0 (documentation
only) is complete and accepted; P1 is partial and reopened for identity
cleanup; P2–P6 are pending. Code baseline: `3544357`, inspected on
2026-10-08.
`../../PLAN.md` Phase 7 owns overall progress; this document states the
goal and boundaries, while [phases.md](phases.md) owns the P1–P6 code
outcomes and gates. `../../ROADMAP.md` owns the later implementation of
actual additional channels.

Accepted decisions live in ADR 0051–0054; a described reference case is
not an executable fixture, and an accepted ADR is not a passing runtime
gate. Anything here may change as new entries land in
`provider-findings/` and `provider-differences/`; claims that looked
closed but depend on that evidence were moved to
[open-decisions.md](open-decisions.md). Cleanup rationale lives in
[domain-cleanup.md](domain-cleanup.md); vocabulary lives in
[domain-terminology.md](domain-terminology.md). Provider-specific
evidence is not a capacity guarantee.

## 1. Outcome: adding a channel is adapter work

Prepare Botflota to operate through messaging channels without assuming
any one provider. WhatsApp becomes an ordinary registered adapter, not
the runtime's implicit account model. Later work on further channels
should concentrate on the adapter, its protocol, credentials, options,
and capability declaration, rather than reopening the messaging
architecture.

- Expected changes: its implementation and tests under `adapters/`,
  one composition-root registration, approved dependencies, and
  adapter-specific examples or guides. Generated schema changes are
  allowed when they follow the registered option types.
- Unexpected changes: transport ports, shared domain records, bot
  declarations, configuration, engine routing, fleet control, generic
  API/CLI handlers, or shared persistence schema/stores. Needing those
  changes to onboard an already-modeled channel means this preparation
  is incomplete.
- A genuinely new capability may justify a separate core extension. It
  must not be disguised as raw protocol JSON, arbitrary commands, or a
  growing provider switch in shared code.

The final gate must demonstrate this rule by registering an independent
test adapter after the shared runtime is in place. Merely implementing
the messaging transport port is insufficient.

## 2. Architectural boundaries

| Owner | Responsibility after preparation |
| --- | --- |
| Core | Neutral messages, addressing, account metadata, errors, and small consumer-owned ports. |
| `spec` | Channel selection and common declarative behavior; no SDK or per-provider bot structs. |
| `config` | Strict decoding, pure support checks, publication, schema, and reload comparison. |
| `engine` | Execute the graph against neutral peers and chats, never protocol rules. |
| `fleet` | Actor lifetime, chat control, pacing, and durable delivery through injected ports. |
| `api` and CLI | Authorized operations over the same neutral account and conversation contracts. |
| Adapter | Protocol, identity mapping, credentials, options, setup, and dynamic eligibility. |
| Composition root | Register definitions and assemble only the selected adapters' dependencies. |

Keep the transport port centered on connect, disconnect, send, and
inbound messages; preparation may refine their shared values and errors
without creating a universal interface for every feature. Binding
lifecycle and interactive attempts belong beside transport, not inside
connection startup. Reuse optional reporting, external-send observation,
and history capabilities where they fit. Do not add unused
receipt/editing ports, runtime plugins, reflection-based registration,
or `init`. The core remains standard-library-only; `config` never
imports adapters or `engine`.

A channel is one bot's logical view of its committed binding, not
ownership of one physical socket or process; adapter-owned shared
resources and context selection stay out of the common contracts. A
bot's usable behavior is bounded by its selected installed adapter and
account mode, with declared support, account authorization, and
conversation eligibility kept as separate facts (ADR 0052). Both
operating paths must work: an operator using YAML and the CLI, and a
host backend using the scoped API for remote account setup and
messaging (ADR 0046). No host credential or reusable channel secret
reaches the browser.

This plan implements architectural preparation, not new production
adapters, provider SDKs, a dashboard, cross-provider peer merging,
shared-account dispatch, or infrastructure provisioning. No dependency
is approved here. Group graph policy and new interactive DSL features
remain separate work; unsupported shared conversations must be refused,
not treated as direct messages (ADR 0051).

## 3. Execution rules

Deliver P1–P6 in order, preserving WhatsApp operation at every phase
boundary. Each phase includes the affected consumers, executable tests,
and compatibility translations needed to keep the tree working; do not
leave public ports changed while their callers remain on the previous
model.

- Check a task only when its code and tests exist; check a phase only
  when its gate passes.
- Update the corresponding `../../PLAN.md` deliverable in the commit
  that completes the phase.
- Transitional WhatsApp translations belong at named compatibility
  boundaries, not throughout the engine, fleet, or stores. They must not
  become a second independent account/setup implementation.
- The owner steers toward a provider-neutral domain, not merely
  optional provider-specific fields; the candidate direction and its
  open questions live in [domain-cleanup.md](domain-cleanup.md) and
  [open-decisions.md](open-decisions.md). Align the canonical ADRs
  before implementing the changed contracts. Existing partial code is
  not evidence that a task is closed.
- Use table-driven `t.Run` tests, hand-written fakes, and `testdata/`
  fixtures. Use fakes early, not only after designing every abstraction
  around the existing WhatsApp adapter.
- Follow repository build permissions and Make targets. Run checks in
  the main repository, never in a worktree; do not infer successful
  tests or manual verification from a design review.
- This is a map of architectural changes and acceptance criteria, not
  method-by-method instructions. Reuse existing code where it fits and
  avoid unrelated refactoring or speculative frameworks.

## 4. Phase index

Summaries only; the checklists and gates live in
[phases.md](phases.md).

- **P1 — Neutral identity, conversations, and durable targets**
  (ADR 0051): identity keys and conversation targets replace
  provider-specific addressing across records, stores, engine, fleet,
  and the WhatsApp mapping.
- **P2 — Extensible definitions, configuration, and selective
  assembly** (ADR 0052, ADR 0054): installed-definition catalog,
  channel selector, adapter-owned options, per-bot capability
  validation, and registered fleet builders.
- **P3 — Provider-neutral binding lifecycle and bind runtime**
  (ADR 0053): neutral binding metadata and generation plus
  inspection/bind/unbind operations; WhatsApp pairing adapts to them.
- **P4 — Common API and CLI** (ADR 0053, ADR 0054): neutral
  lifecycle/discovery/bind operations over scoped routes; legacy
  WhatsApp shapes survive only as explicit compatibility translations.
- **P5 — Delivery, replay, and optional operations** (ADR 0052):
  classified send outcomes, admission plus generation checks, and
  replay/cursor boundaries without protocol leakage.
- **P6 — Extension locality and regression coverage**: an independent
  test adapter proves new channels need no shared-code changes;
  documentation and schema match the code.

## 5. Document map

- [phases.md](phases.md): P1–P6 checklists and gates.
- [domain-cleanup.md](domain-cleanup.md): candidate cleanup direction,
  compatibility approach, closing evidence, ADR alignment.
- [open-decisions.md](open-decisions.md): reopened claims and gaps
  waiting on provider evidence or owner calls.
- [domain-terminology.md](domain-terminology.md): current-to-target
  naming catalog. Naming a replacement does not implement it, close a
  runtime gate, or authorize an incompatible public representation.
- `provider-findings/`: per-channel investigation notes with outbound
  send contracts and sources.
- `provider-differences/`: per-area comparisons across channels and
  account modes; most are stubs to fill as findings land, except the
  outbound-messages matrix.
