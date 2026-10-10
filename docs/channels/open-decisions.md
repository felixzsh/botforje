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

## Reopened claims waiting on provider evidence

- **Common optional phone metadata: keep or remove.** ADR 0051 keeps
  phone as optional metadata, never identity; the 2026-10-08 owner
  steer removes provider-specific concepts from shared contracts even
  as optional fields. Decide which reading wins before P1 closes.
  Depends on: `provider-differences/identity-directory-targets.md` and
  the identity notes in each file under `provider-findings/`. Owner:
  ADR 0051, ADR 0029.
- **Peer-card replacement format.** The vCard-subset replacement was a
  recommendation, not an agreed contract. Telegram's card requires a
  phone and first name, Signal and Cloud cards have their own shapes,
  and SimpleX has no telephone card. Agree version/subset, templating,
  limits, and validation before P2 codes it. Depends on:
  `provider-differences/outbound-messages.md` and the contact-card
  sections of each finding. Owner: ADR 0036.
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
- **Identity-space composition rule.** Whether adapter-normalized
  subjects/threads must always fold in their durable identity space,
  and what compositions are allowed, is a candidate rule awaiting
  counter-examples from new providers. Depends on:
  `provider-differences/identity-directory-targets.md`. Owner:
  ADR 0051.

## Operational gaps carried over from the plan

- **P1, addressing and default reply.** Settled: identities use
  `(scope, provider, subject)`; conversations use
  `(scope, bot, provider, kind, thread, topic)`; an alternate `to`
  keeps the triggering chat's history while its queue entry carries the
  destination's own conversation. Not settled: account/phone cleanup,
  participant mapping, and destination-dedup/restart gates. Owner:
  ADR 0051, ADR 0026.
- **P2, selector ambiguity.** ADR 0054's provider/mode selector must
  resolve the adapter kind unambiguously. Decide how indistinguishable
  installed implementations are rejected or selected before supporting
  them; do not invent precedence or turn adapter kind into an account
  mode.
- **P1/P3/P4, directory provenance.** The scope-wide identity registry
  and binding-owned directory must not be conflated. Current history
  migration transfers directory rows to another bot. Decide
  retention/reimport/mapping after unbind, identity replacement, and
  history migration before changing ownership. Imported or moved
  directory data is not evidence of current membership or permission.
  Owner: ADR 0037, ADR 0042, ADR 0051.
- **P3/P5, status and generations.** Decide which confirmed facts are
  durable and which observations are live or stale; do not persist
  yesterday's successful pairing as current readiness. Request revision,
  config reload generation, and binding generation are separate. Specify
  how association provenance survives delete/rebind for directories and
  queued sends. Owner: ADR 0051, ADR 0053, ADR 0040.
- **P2/P4, public compatibility.** Select neutral HTTP resources and the
  translation/versioning of old commands, paths, template roots,
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
