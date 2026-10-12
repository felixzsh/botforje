# Open decisions for channel preparation

Status: working list, not approved for implementation. Nothing here
authorizes incompatible behavior in code; each item must be resolved
with the owner and recorded in its owning ADR or document before the
affected slice is coded.

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
  The destination-dedup and close/reopen/restart gates pass. The per-bot
  WhatsApp translation of legacy `contact.*` template references moves to P2,
  which owns the consuming bot's definition; the account-scoped durable dedup
  key stays a P5 decision.
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
- **P2/P4, public compatibility.** P1 template policy is selected:
  per-bot WhatsApp translation preserves `contact.id/name/phone` through
  neutral peer paths without a universal phone value (ADR 0018/0020).
  Implemented on 2026-10-12: the WhatsApp definition declares the alias
  table, config checks a shared action in each consuming bot's context, and
  the engine translates at resolution. Other adapters do not inherit those
  aliases and refuse them with the bot, graph, node, and action. Still select
  neutral HTTP resources and compatibility of other commands, paths,
  filter/card keys, result fields, event topics, and archive sections.
  The rename catalog owns names, not permission to change serialized
  meaning. A first allowed inbound is not directory insertion; keep its
  durable timing and signed retry bytes intact. Do not add a parallel
  lifecycle facade under the old vocabulary. Owner: ADR 0018, ADR 0044,
  ADR 0045, ADR 0053, ADR 0054.
- **P5, history and external observations.** Source retrieval,
  traversal/exhaustion, and persisted import outcome must remain
  distinct. An emission outside this runtime is not necessarily
  human-originated. Settle normalized source and provenance boundaries
  with actual consumers before claiming support. Owner: ADR 0035,
  ADR 0043, ADR 0052.
- **P4/P6, persistence and export boundary.** Domain history grouping
  must not mirror SQL table names. Keep physical schema, archive
  filenames, and legacy formats in their adapters; approve format
  changes independently of Go renames. Owner: ADR 0035, ADR 0042.
- **P5, unbound sends.** ADR 0044 currently accepts sends to an unpaired
  bot, but the new queue must be tied to the account authorized at
  acceptance. Decide whether no-binding admission is rejected or has
  another explicit safe contract; it cannot automatically attach to a
  later identity. Owner: ADR 0044.
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
