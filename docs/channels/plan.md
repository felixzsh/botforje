# Messaging channel preparation plan

Status: revised draft after comparative research review. The evidence
is sufficient to proceed with phase-local design and implementation
once the affected decisions are approved; another broad investigation
is not a prerequisite. This does not approve unresolved contracts or
close runtime gates. P0 (documentation only) is complete and accepted;
P1 is partial and reopened for identity cleanup; P2–P6 are pending.
Code baseline: `3544357`, inspected on 2026-10-08; this research review
is not a new code audit.
`../../PLAN.md` Phase 7 owns overall progress; this document states the
goal and boundaries, while [phases.md](phases.md) owns the P1–P6 code
outcomes and gates. `../../ROADMAP.md` owns the later implementation of
actual additional channels.

The comparative input is
[differences-investigation-results.md](differences-investigation-results.md),
with the earlier outbound comparison in `provider-differences/`.
ADRs, this plan, phase checklists, and candidate domain documents are
all revisable against that evidence. An accepted ADR is a decision
baseline, not a constraint the investigation must justify; revise its
canonical text before implementing a changed contract. A research
claim is not an executable fixture or a passing runtime gate.
[Open decisions](open-decisions.md) owns unresolved choices;
[domain-cleanup.md](domain-cleanup.md) owns cleanup rationale and
[domain-terminology.md](domain-terminology.md) owns vocabulary. Existing
statements in those documents are not automatically closed by this
review. Provider-specific evidence is not a capacity guarantee.

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

## 3. Research assessment and decision gates

### Sufficient evidence for preparation

The report compares WhatsApp native and Cloud, Telegram bot and user
login, Signal through signalmeow, and SimpleX through its CLI API. They
are contrast cases for the common domain, not a closed provider taxonomy
or approval to install their dependencies. Outbound payload research
need not be repeated.

The report is sufficient to expose the assumptions this preparation
must remove: authorization is not transport health; identity is not a
destination; acceptance is not delivery; event backlog is not complete
history; and an own message is not proof of Botflota or human authorship.
Existing phase boundaries broadly fit those concerns, but their
contracts must be reviewed rather than implemented unchanged.

The report did not inspect this plan or its ADRs and ran no live tests.
Its citation tokens refer to an external research session and currently
have no source-link mapping in the repository. Treat it as comparative
input, not independently verified support for every version-specific
claim. Recover primary references for facts used to change a contract
or declare a real capability, reusing matching sources in
`provider-findings/` or checking the specific claim. Missing citation
export is a traceability gap, not a reason to repeat the entire study.

### Decisions belong to the affected phase

Use the report to review these boundaries with the owner. The ADRs
listed below identify where revised decisions belong, not immutable
answers. Resolve a choice before coding its affected contract; do not
wait for unrelated real-adapter research to finish every phase.

- **P1 — Identity and destinations:** separate comparable identity
  from the context needed to reach a conversation. A stable external
  ID need not be a usable destination under another authorization.
  Protocol resolution and credential-bearing routing state stay
  adapter-owned. Review local identity-space composition, participant
  mapping, directory provenance, and migration eligibility; a binding
  generation is not automatically an identity or message namespace.
  Align ADR 0051 and the phone/card decisions already recorded in
  `open-decisions.md` with their owning ADRs before reusing old shapes.
- **P2 — Declared support:** validate the installed implementation and
  account mode, not a provider-wide feature list. Separate implemented
  support, account authorization, and current conversation eligibility.
  Names such as presence, read, or groups do not establish equivalent
  effects. Review declaration granularity and selector ambiguity in
  ADR 0052/0054; new optional operations still need a real consumer.
- **P3 — Binding lifecycle:** distinguish confirmed association,
  authorization, transport availability, and recoverable local state.
  Review stale observations, identity renewal/replacement, and local
  detachment versus remote revocation or profile destruction in
  ADR 0053. A shared physical resource is not a shared bot identity.
- **P4 — Public meaning:** settle neutral representations and explicit
  compatibility or breaking changes with their owning ADRs before
  changing serialized meaning. Neither a rename nor preserving an old
  field justifies keeping a provider-specific assumption in the domain.
- **P5 — Delivery and reception:** choose acceptance/status translation,
  uncertain-outcome retry policy, message-ID/dedup scope, unbound-send
  admission, and processing completion in ADR 0004/0026/0040/0044/0052.
  Queue handoff, provider acknowledgement, and durable processing are
  different boundaries. Distinguish inert history import from live
  recovery; confirm only the explicitly agreed processing outcomes.
  Review ADR 0043's operator attribution and pause policy: an
  uncorrelated own send may come from another automation, not a person.
  Correlate all Botflota origins, including API sends, and review late
  echoes and restart without assuming every channel emits an echo.
- **P6 — Conformance:** exercise those distinctions through the same
  engine, stores, fleet, and API paths with contrasting fakes. Include
  authorization without a persistent socket, context-local targets,
  acceptance without receipts, finite replay with processing completion,
  ambiguous own-send provenance, and shared-view backpressure. These
  are behavioral cases, not six production adapters or provider branches;
  the independent extension-locality gate in `phases.md` still applies.

Acceptance-only delivery and richer optional receipts are design
alternatives, not research mandates. Do not add receipt machinery without
its consumer, or present missing receipts as failure. At-least-once
retry does not promise recipient delivery, presentation, or exactly-once
execution of external effects. Review any stronger existing claim in its
owning ADR rather than copying it into the neutral contract.

### Targeted verification, not a global research blocker

The report's residual uncertainties concern Cloud coexistence/groups
and webhook ordering, Signal history transfer and multi-device receipts,
SimpleX multi-client event distribution and receipt detail, and WhatsApp
sync reliability. They gate the relevant real adapter or stronger
observable guarantee, not the whole preparation. Unknown behavior must
stay unknown; no fake or declaration may turn it into supported behavior.

New credentials, trusted auxiliary endpoints, service ownership, and
operational/legal acceptance remain real-adapter decisions under
`open-decisions.md`, ADR 0053/0054, and `../../ROADMAP.md`. Preparation
uses synthetic state and tests cancellation, teardown, and bounded
backpressure without promising independent throughput or lossless
provider replay. Record incidental evidence in `provider-findings/`;
reopen a common boundary only when a concrete finding requires it.

## 4. Execution rules

Deliver P1–P6 in order after the affected decision review, preserving
WhatsApp operation at every phase boundary or applying an explicitly
approved compatibility change. Each phase includes the affected
consumers, executable tests, and compatibility translations needed to
keep the tree working; do not leave public ports changed while their
callers remain on the previous model.

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
  [open-decisions.md](open-decisions.md). Review those documents and
  `phases.md` against section 3 for the next slice, resolving conflicts
  and aligning the canonical ADRs before implementing changed
  contracts. This review does not silently amend their decisions or
  checklists. Existing partial code is not evidence that a task is closed.
- Use table-driven `t.Run` tests, hand-written fakes, and `testdata/`
  fixtures. Use fakes early, not only after designing every abstraction
  around the existing WhatsApp adapter.
- Follow repository build permissions and Make targets. Run checks in
  the main repository, never in a worktree; do not infer successful
  tests or manual verification from a design review.
- This is a map of architectural changes and acceptance criteria, not
  method-by-method instructions. Reuse existing code where it fits and
  avoid unrelated refactoring or speculative frameworks.

## 5. Phase index

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

## 6. Document map

- [phases.md](phases.md): P1–P6 checklists and gates.
- [domain-cleanup.md](domain-cleanup.md): candidate cleanup direction,
  compatibility approach, closing evidence, ADR alignment.
- [open-decisions.md](open-decisions.md): reopened claims and gaps
  waiting on provider evidence or owner calls.
- [domain-terminology.md](domain-terminology.md): current-to-target
  naming catalog. Naming a replacement does not implement it, close a
  runtime gate, or authorize an incompatible public representation.
- [differences-investigation-results.md](differences-investigation-results.md):
  imported six-topic report, implications, and residual uncertainties;
  see section 3 for its evidence limits and readiness assessment.
- `provider-findings/`: per-channel evidence, sources, and incidental
  discoveries, including findings outside the topic being compared.
- `provider-differences/`: per-area comparisons across channels and
  account modes. Outbound is developed; the remaining files are still
  stubs, not proof that the new report lacks those comparisons. Populate
  them from traceable findings as needed, without duplicating the report
  or making that editorial work a prerequisite for all implementation.
