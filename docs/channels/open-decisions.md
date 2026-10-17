# Open decisions for channel preparation

Status: phase-local decision register. Open entries are not approved for
implementation; each must be resolved with the owner and recorded in its owning
ADR before the affected slice is coded. Closed entries record that approval,
not passing implementation gates. P4's public contracts and P5's
delivery/reception/provenance contracts are closed below; future real-adapter
decisions remain open.

Several items below were previously stated as closed in `plan.md`.
They are reopened here because their answers depend on provider
differences and findings gathered later. Each such item names the
evidence it is waiting for. New findings in `provider-findings/` or new
rows in `provider-differences/` should add or reopen entries here, not
silently harden the plan.

## Closed on 2026-10-10 (owner decisions; implementation is phase-tracked)

- **Phone-number term: eliminate it from the domain, keep it as a payload
  label only.** This is the selected target, not a claim about the current
  checkout. Identity/addressing/store/template cleanup takes effect when
  channels P1 is implemented; remaining setup/result cleanup belongs to P3.
  The target shared contracts have no universal `Phone` field, parameter,
  column, helper, digit validation, or template path. The `phone` contact-card
  entry label carries opaque payload data that shared code never parses.
  Phone-like values can occur inside opaque subjects or message text without
  becoming telephone metadata. Setup inputs remain adapter-owned; handset
  mentions in prose are not domain contracts. ADR 0051, 0018, 0020, 0021,
  and 0029 now describe the P1 target. Its implementation and gates remain
  unchecked in [phases.md](phases.md).
- **Peer-card declaration shape.** `spec.Contact` is now
  `name` + `entries[{kind, value, label}]` + `note`, with kinds `phone`,
  `email`, `address`, `url`; `botflota.ContactContent` mirrors it and
  the stored codec round-trips it. vCard composition and parsing live
  entirely in the WhatsApp adapter; the core never imports vCard
  concepts. Aligned on 2026-10-12: ADR 0036 describes the neutral card and
  its per-adapter variants, `Capabilities.CardVariants` drives the per-bot
  check, and the WhatsApp definition declares all four kinds.
- **No separate phone/email steps.** No provider exposes "send a phone
  number" or "send an email" as a message object; every card object
  bundles a person's attributes. A bare number or address as text is
  already a `message` step. Connection/invitation links are a different,
  future `invite` kind with no consumer today, not a contact variant.

## Closed on 2026-10-12 — P4 public contracts

The owner approved the following choices after P3 review. Public API/CLI names,
arguments, query parameters, fields, counts, and exports must match the selected
internal domain vocabulary, even when a Go rename is still pending. These are
contract decisions, not completed P4 code or gates:

- **Resources and commands.** Scope peers and bot-owned directory, chats, and
  graph runs use the selected domain names. CLI binding operations are
  `bot bind` and `bot unbind`, without old aliases. Binding and setup routes use
  `/bots/{bot}/binding` and `/binding/setups/{id}`, explicitly replacing the
  earlier `/account/enrollments` proposal. Owners: ADR 0054 and ADR 0053.
- **References and send selectors.** Public identities and targets use the
  neutral P1 shapes. Reads/control/sync resolve chat IDs under scope and bot;
  sends take exactly one of `chat_id` or explicit `target`, with an optional
  separate known `recipient`. Graph references are `graph_run_id`; no phone or
  ambiguous graph-session projection survives. Owner: ADR 0044, ADR 0054.
- **Discovery, readiness, and unsupported operations.** Declared capabilities,
  binding readiness, authorization, transport, and target eligibility remain
  separate facts. Missing observations are explicitly unknown; an absent
  operation is 422 `unsupported_operation`, not 500. Stored history/directory
  reads do not imply live import support. Owners: ADR 0052 and ADR 0053.
- **Export.** Keep streaming, snapshot-consistent ZIP/CSV with empty-section
  headers. Domain sections are `chats`, `graph_runs`, `messages`, `directory`;
  the format adapter owns filenames/encoding. Export neutral identities,
  targets, and recorded directory origins, without speculative P5 provenance
  fields, credentials, a legacy reader, or a compatibility archive. ADR 0042
  fixes the filenames and headers; P6 verifies implementation conformance.
- **No-binding send admission.** A new send without a committed binding is
  rejected before persistence or queueing with 409 `binding_required`.
  Disconnected does not mean unbound; an already accepted `client_id` retry is
  reconciliation, not a new admission. Owner: ADR 0044. P4 implements the API
  boundary; P5 still owns durable binding snapshots and delivery checks.
- **Webhook alignment.** `peer.first_seen` and its neutral peer envelope are
  already selected and implemented. ADR 0045 now matches them; P4 keeps the
  durable first-inbound timing and byte-stable signed retries, not a legacy
  topic or phone projection.

No unresolved public-contract choice listed above blocks starting P4. Do not
pull receive-completion, message-ID namespace, or production shared-service
policy into P4; the P5 contracts are closed in the section below, and
production shared-service policy remains open.

## Closed on 2026-10-17 — P5 delivery, reception, and provenance

The owner approved seven contracts, closing the P5 items that `phases.md`,
ADR 0004/0040, ADR 0026/0051, ADR 0043/0035, and ADR 0052 left open. These are
contract closures recorded in their owning ADRs, not passing implementation
gates; P5 and P6 code remains pending.

- **Receive/processing completion.** ADR 0004/0040: a source that confirms
  updates gets an optional per-entry outcome of committed, already-recorded
  duplicate, deliberate discard, or failed processing. Queue or goroutine
  handoff confirms nothing, a failure never confirms, a cumulative cursor never
  passes a failed entry, and a held inbound confirms after its history is
  stored. Confirming promises no exactly-once execution of external side
  effects.
- **Message-ID space and durable dedup key.** ADR 0026/0051: every message
  records an adapter-declared message space (the provider/account/profile
  context needed for uniqueness); the durable key is scope, bot, that space,
  the complete destination, and the provider id. The space survives
  reconnect/restart/reauthorization, a different account is a different space, a
  generation change does not by itself change it, and moved history keeps its
  provenance. Binding generation is delivery authorization, not a message-ID
  namespace.
- **Own-send attribution and pause.** ADR 0043: handing over is a conservative
  best-effort reaction to an observed external send, not human attribution.
  Correlation covers every Botflota send including API-origin, holds within a
  bounded in-memory window, and is not claimed across restart or for adapters
  that cannot separate their own echo. Imported history never triggers a pause,
  and unknown provenance stays unknown.
- **Shared-source backpressure.** ADR 0052/0040: bounded buffers, no silent
  drop, cancelable waits, releasing one logical view without closing peers, and
  coupled consumer progress. Grouping, fairness, capacity, and durable
  buffering stay deployment decisions, and Botflota promises no provider
  losslessness.
- **History and external-observation provenance.** ADR 0026/0035/0043: ingest
  kind, known origin, and the recorded message space are preserved. Import is
  inert, a live replay of pending traffic follows normal processing, and a
  history move never rewrites provenance as the current account's.
- **Uncertain send outcome.** ADR 0052/0040: a timeout after possible
  acceptance is uncertain, not a proven rejection. It retries at-least-once and
  its terminal record says acceptance was not confirmed; an idempotency key or
  pre-assigned id is reused for that delivery's attempts.
- **Capability granularity.** ADR 0052: per installed definition and account
  mode, never per provider, with declared support, account authorization, and
  current eligibility kept separate.

## Reopened claims waiting on provider evidence

- **Common optional phone metadata: keep or remove.** Closed on
  2026-10-10, see the Closed section above: removed, except the
  `phone` payload label on contact entries. P1-owning ADRs are aligned;
  implementation and later setup/result work remain phase-tracked.
- **Peer-card replacement format.** Closed on 2026-10-10, see the
  Closed section above: neutral entries shape with adapter-owned vCard
  handling. Closing evidence added 2026-10-12: ADR 0036 aligned and the
  per-bot card-variant capability check implemented.
- **Capability granularity.** Closed on 2026-10-17, see the P5 section above:
  per installed definition and account mode, never per provider (ADR 0052).
- **Receive completion signature.** Closed on 2026-10-17, see the P5 section
  above: committed, already-recorded duplicate, deliberate discard, and failed
  processing (ADR 0004, ADR 0040). No universal durable inbox is approved.
- **Durable message-ID and dedup key.** Closed on 2026-10-17, see the P5
  section above: the adapter-declared message space plus the complete
  destination plus the provider id, scoped by scope and bot, with moved history
  keeping its provenance (ADR 0026, ADR 0051).
- **Shared-source buffering and backpressure.** Closed for the preparation on
  2026-10-17, see the P5 section above: bounded buffers, no silent drop,
  cancelable waits, coupled progress, and no provider-losslessness promise
  (ADR 0052, ADR 0040). Deployment grouping, fairness, capacity, and durable
  buffering remain TBD under ADR 0046/0054.
- **Identity-space composition rule.** The P1 design in ADR 0051 requires
  store/profile-local IDs to include their durable space in adapter-normalized
  subjects and threads, with a documented unambiguous encoding. Provider-wide
  IDs need no artificial prefix; common code never parses either form.
  Real adapters must establish how their space survives restart/renewal and
  changes on unrelated replacement. New counter-examples can reopen the rule;
  they do not require a speculative second field in every identity key.

## Operational gaps carried over from the plan

- **P1, identity and addressing implemented on 2026-10-11.** ADR 0051
  specifies comparable key/target fields, canonical absent topic, scoped
  lookups, valid live senders, optional explicit participants, normalized
  own account identity versus adapter device lookup, and actual outbound
  targets. ADR 0026/0029 separate graph execution and first-live-inbound.
  The destination-dedup and close/reopen/restart gates pass. The retired
  `contact.*` template references get no replacement: the neutral `peer` root
  is the only one, and an old reference fails as an unknown root (owner
  decision 2026-10-12). The account-scoped durable dedup key closed on
  2026-10-17 (ADR 0026/0051).
- **P2, selector ambiguity.** Closed on 2026-10-12 (owner decision):
  two installed definitions that share a provider and mode are rejected when
  the catalog is built, and the adapter kind never participates in selection
  or precedence. Recorded in ADR 0054 and implemented in `channels.New`;
  verified by the catalog tests. The neutral HTTP/CLI form of the selector
  stays a P4 item.
- **P1 directory provenance closed.** The owner chose historical snapshots
  with explicit source account identity (ADR 0037/0042). A compatible migration
  moves data ownership without rewriting origin or claiming current
  membership. Binding generations are decided in ADR 0053, and the public
  representations are closed in the P4 section above; no P1 source identity
  alone establishes those guarantees.
- **P3/P5, status and generations.** Closed on 2026-10-17: readiness is a live
  observation of the committed binding and local credential presence, so a
  stored pairing transition is never presented as current readiness (ADR 0053),
  and a setup session's `revision`, a binding's `generation`, and the active
  config snapshot are independent values (ADR 0053, ADR 0009). Directory
  provenance survives delete/rebind through its preserved source identity
  (ADR 0042), and queued sends through their accepted binding generation
  (ADR 0040). Queue-side enforcement is P5 implementation, not an open
  decision. Owners: ADR 0009, ADR 0040, ADR 0042, ADR 0051, ADR 0053.
- **P2/P4, public compatibility.** Closed on 2026-10-12 (owner decision):
  the preparation ships no backward compatibility. A superseded template root,
  settings key, selector, route, event, or archive section is removed with its
  owning phase and fails validation with a precise error; it is never
  translated, aliased, or accepted as a no-op. `contact.*` is gone with no
  replacement (ADR 0018/0020). The neutral resources, selectors, views, CLI
  names, and archive contract are now also closed in the P4 section above.
  The selected serialized contact-card kind/key remains `contact` (ADR 0036),
  not a peer identity. A first allowed inbound is not directory insertion;
  keep its durable timing and signed retry bytes intact. Owner: ADR 0018,
  ADR 0044, ADR 0045, ADR 0053, ADR 0054.
- **P5, history and external observations.** Closed on 2026-10-17, see the P5
  section above: ingest kind, known origin, and the recorded message space are
  preserved; import is inert, live replay is normal processing, and an emission
  outside this runtime is never attributed to a person without evidence
  (ADR 0026, ADR 0035, ADR 0043).
- **P4/P6, persistence and export boundary.** Closed on 2026-10-12:
  ADR 0042 owns the domain-section ZIP/CSV contract above. Physical schema
  and format filenames stay out of shared history ports. P4 implementation
  and P6 conformance remain pending; no legacy reader/archive is approved.
- **P4/P5, unbound sends.** Closed on 2026-10-12: ADR 0044 requires a committed
  binding for new admission and rejects its absence with 409 `binding_required`.
  Pending code must apply that decision; durable queue snapshots, generation
  checks, and replacement safety still belong to P5.
- **P5, receive commit boundary.** Closed on 2026-10-17, see the P5 section
  above: the narrow acknowledgement contract is the four-outcome completion of
  ADR 0004/0040. A channel handoff is still not a durable inbox or a committed
  turn, and a provider update ID/cursor is still not a persisted message ID.
- **Future shared adapters and deployment.** Decide trusted service
  ownership, grouping, shutdown, exclusive command coordination, and the
  supported overload/replay boundary before deploying a shared source.
  Owner: ADR 0046, ADR 0054. The preparation uses fakes, not a
  production service manager.

## Standing gates for future real adapters

These do not block preparation, but they gate the corresponding future
adapter; fakes use synthetic inputs:

- Credential encryption and key custody before storing a new real
  credential type. Owner: ADR 0053.
- Trusted auxiliary endpoint and profile deployment. Owner: ADR 0054.
- A scoped caller must never choose arbitrary server paths, databases,
  or unmanaged endpoints. Do not assume ADR 0047–0049's file-root,
  egress, or secret-catalog protections are implemented.
