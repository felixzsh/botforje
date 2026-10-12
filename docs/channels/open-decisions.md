# Open decisions for channel preparation

Status: phase-local decision register. Open entries are not approved for
implementation; each must be resolved with the owner and recorded in its owning
ADR before the affected slice is coded. Closed entries record that approval,
not passing implementation gates. P4's public contracts are closed below;
P5 and future real-adapter decisions remain open.

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
policy into P4; close those contracts before their own consumers are coded.

## Reopened claims waiting on provider evidence

- **Common optional phone metadata: keep or remove.** Closed on
  2026-10-10, see the Closed section above: removed, except the
  `phone` payload label on contact entries. P1-owning ADRs are aligned;
  implementation and later setup/result work remain phase-tracked.
- **Peer-card replacement format.** Closed on 2026-10-10, see the
  Closed section above: neutral entries shape with adapter-owned vCard
  handling. Closing evidence added 2026-10-12: ADR 0036 aligned and the
  per-bot card-variant capability check implemented.
- **Capability granularity.** Declaring support per channel *and*
  account mode (not per provider) is the working assumption, motivated
  by WhatsApp native versus Cloud and Telegram bot versus user login.
  Confirm it holds as findings grow, or revise it here. Depends on:
  `provider-differences/outbound-messages.md` and
  `provider-differences/optional-operations.md`. Owner: ADR 0052.
- **Receive completion signature.** What "durable processing" means for
  a replay/cursor source — confirmed processing, persisted duplicates,
  deliberate discards, failures — and which function signature carries
  it are still TBD; no universal durable inbox is approved. Depends on:
  `provider-differences/history-and-recovery.md`,
  `provider-differences/delivery-and-receipts.md`. Owner: ADR 0004,
  ADR 0040. Gates P5.
- **Durable message-ID and dedup key.** Account-local IDs must not
  collide with retained history after replacement; binding generation
  alone is not proof of the provider's message-ID namespace. Depends
  on: `provider-differences/history-and-recovery.md` and the per-finding
  send contracts. Owner: ADR 0051, ADR 0026. Gates P5.
- **Shared-source buffering and backpressure.** Logical data isolation
  does not imply independent throughput or failure domains; bounded
  buffers, replay versus buffering, fairness, and grouping are all
  unsettled. Depends on:
  `provider-differences/delivery-and-receipts.md`,
  `provider-differences/binding-lifecycle.md`, and
  `provider-findings/simplex.md`. Owner: ADR 0046, ADR 0054. No
  supervisor, per-profile spool, fairness promise, or capacity figure is
  approved.
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
  decision 2026-10-12). The account-scoped durable dedup key stays a P5
  decision.
- **P2, selector ambiguity.** Closed on 2026-10-12 (owner decision):
  two installed definitions that share a provider and mode are rejected when
  the catalog is built, and the adapter kind never participates in selection
  or precedence. Recorded in ADR 0054 and implemented in `channels.New`;
  verified by the catalog tests. The neutral HTTP/CLI form of the selector
  stays a P4 item.
- **P1 directory provenance closed; P3/P4 work remains.** The owner chose
  historical snapshots with explicit source account identity (ADR 0037/0042).
  A compatible migration moves data ownership without rewriting origin or
  claiming current membership. Full binding generations, freshness after
  replacement, and public representations remain their phase's decisions;
  no P1 source identity alone establishes those guarantees.
- **P3/P5, status and generations.** Decide which confirmed facts are
  durable and which observations are live or stale; do not persist
  yesterday's successful pairing as current readiness. Request revision,
  config reload generation, and binding generation are separate. Specify
  how association provenance survives delete/rebind for directories and
  queued sends. Owner: ADR 0051, ADR 0053, ADR 0040.
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
- **P5, history and external observations.** Source retrieval,
  traversal/exhaustion, and persisted import outcome must remain
  distinct. An emission outside this runtime is not necessarily
  human-originated. Settle normalized source and provenance boundaries
  with actual consumers before claiming support. Owner: ADR 0035,
  ADR 0043, ADR 0052.
- **P4/P6, persistence and export boundary.** Closed on 2026-10-12:
  ADR 0042 owns the domain-section ZIP/CSV contract above. Physical schema
  and format filenames stay out of shared history ports. P4 implementation
  and P6 conformance remain pending; no legacy reader/archive is approved.
- **P4/P5, unbound sends.** Closed on 2026-10-12: ADR 0044 requires a committed
  binding for new admission and rejects its absence with 409 `binding_required`.
  Pending code must apply that decision; durable queue snapshots, generation
  checks, and replacement safety still belong to P5.
- **P5, receive commit boundary.** If a replay/cursor channel needs a
  durable-processing signal, settle the narrow acknowledgement contract
  in ADR 0004/0040 before closing P5. Do not silently promote a channel
  handoff into a durable inbox or a committed turn. Exact signatures,
  deliberate-discard rules, and failed-processing behavior remain TBD;
  distinguish a provider update ID/cursor from a persisted message ID.
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
