# Channel-neutral domain cleanup direction

Status: candidate direction, not a closed decision and not approved
for implementation. It records where the cleanup is heading so
[phases.md](phases.md) slices stay coherent. Every statement here is
provisional: later entries in `provider-findings/` and
`provider-differences/` can reopen it, and the owning ADRs must be
aligned before the affected slice is coded. Items whose answer depends
on that evidence were moved to [open-decisions.md](open-decisions.md).

P1's identity, target, peer, directory, and outbound-provenance cleanup landed
on 2026-10-11; the binding, card, history-section, and API/CLI neutral
reference work remains in their phases.

Owner steer (2026-10-08): binding, peer, chat, and execution contracts
should not contain messaging-provider-specific concepts. A phone is not
a universal messaging identity, and the target is a provider-agnostic
domain with inward dependency boundaries — not a union of today's
providers and not a renamed WhatsApp model. Future providers may bring
identities and flows none of the studied channels use; they must not
inherit assumptions from those examples. Removal of common optional phone
metadata is an owner decision. The selected P1 contracts and template/directory
rules are now recorded in their canonical ADRs; their implementation remains
pending, as distinguished in [open-decisions.md](open-decisions.md).

## Principles for the common contract

- Domain language follows the runtime's responsibilities, not a
  provider's API nouns. Renaming a field without removing its
  provider-specific meaning does not make the boundary neutral. The
  naming inventory lives in [domain-terminology.md](domain-terminology.md),
  not in a second dictionary here.
- Dependencies point inward: the core owns the contracts its consumers
  need; adapters implement them, and the composition root supplies
  implementations. The core never imports SDKs, adapter packages,
  SQL/YAML representations, protocol commands, or transport-specific
  lifecycle rules. Common engine/fleet/API code consumes ports, not
  provider stores or adapter implementation types.
- Shared persistence encodes the domain contract; an SDK schema or wire
  object does not define that contract. Config validation consumes
  injected pure declarations, not a live provider connection. Setup,
  delivery, persistence, presentation, and interactive challenges stay
  separate.
- Provider/adapter identifiers are open catalog values, not a closed
  enum of today's services or conditional branches in generic code. A
  catalog entry names an implementation, not a domain variant with its
  own optional fields. Keep ports small and owned by actual consuming
  operations.
- Model the invariants the runtime needs, not every feature a
  hypothetical provider might offer. A new provider fitting existing
  capabilities should need adapter work plus registration only. A
  genuinely new behavior may require an explicit neutral domain
  extension with its first real consumer; that does not justify a
  raw-command escape hatch or a speculative universal framework.
- Authorization associates a scoped bot with a verified external
  identity, not a companion device, credential, endpoint, challenge, or
  display label. Association, authorization, transport health, and a
  temporary interactive attempt have separate lifetimes; no
  paired/unpaired boolean replaces those facts. Import completion must
  not be mistaken for successful binding.
- Identity and destination are distinct opaque values, normalized by
  their adapter. The core may compare and persist them, not parse
  protocol syntax or infer one from the other.
- The scope's identity registry and a binding-owned directory are
  different relationships. Receiving a message can register an identity
  without adding directory membership. Importing a directory or sharing
  a card does not invent chats, execution state, or first-live-inbound
  activity. Directory membership never grants send permission and does
  not prove current reachability.
- A device/session lookup belongs to the adapter. Reuse its existing
  SDK store; add a persistent bot-to-device relation only if no
  existing link can recover it safely after restart. A renamed device
  locator is still not a verified account identity.
- Local IDs include their durable identity space in adapter-normalized
  subjects and threads, using a documented unambiguous composition (ADR 0051).
  Never derive that space from a bot name, port, path, active profile,
  connection, or reconnect counter. Provider-wide IDs need no artificial
  prefix. New concrete findings may reopen the rule, not silently change it.
- Sender, participant, conversation, and account are distinct roles. A
  direct thread does not universally identify its participant. Use the
  supplied sender/participant or an adapter mapping; never derive a
  participant identity from the thread in generic code. Keep unknown
  identities unknown. Durable history, external addressing, and bounded
  execution have different lifetimes.
- Display names are optional presentation, not identity. Telephone,
  username, profile, and device properties do not become universal
  fields, including in read models. Adapters normalize names; their
  source-specific name provenance and precedence remain outside generic
  storage/runtime. Shared records, ports, read projections, and SQL
  columns carry normalized presentation only. Do not substitute
  unchecked metadata maps, protocol JSON, or a provider-specific union.
- A normalized WhatsApp subject may contain phone digits. That is an
  opaque value, not a promise that all subjects are telephone numbers.
  No generic digit validation, dial rules, suffix stripping, phone
  fallback, or subject-to-thread conversion follows from it.
- Typed message content remains neutral. Telephone data inside user
  text or a standard peer card is payload data, never a peer/binding
  key or an implicit permission to send.

## Contrast cases (links, not copies)

The studied channels constrain the abstraction; they are not a
taxonomy and they contribute no domain vocabulary. Per-channel detail
lives in `provider-findings/`; per-area comparison lives in
`provider-differences/`. Constraints to keep in mind while reading
them:

- WhatsApp: PN normalization and device JIDs stay adapter-owned; see
  [whatsmeow](../provider-findings/whatsmeow.md) and
  [whatsappcloud](../provider-findings/whatsappcloud.md).
- Telegram: Bot API token setup versus user login are separate account
  modes with separate eligibility; see
  [telegram](../provider-findings/telegram.md).
- Signal: number privacy, mutable usernames, and typed service
  identities; see [signal](../provider-findings/signal.md).
- SimpleX Chat: one fixed verified local profile per bot, store-local
  ids, managed auxiliary transport; see
  [simplex](../provider-findings/simplex.md).
- Capability matrix: [outbound-messages](../provider-differences/outbound-messages.md).
  The remaining per-area notes (identity, binding, delivery, history,
  external messages, optional operations) are stubs to fill as findings
  land; their answers feed [open-decisions.md](open-decisions.md).

These cases need no provider enum, provider-specific field, universal
credential table, or SDK dependency in the domain. Fakes represent
their differing identity semantics; real adapters remain future work in
`../../ROADMAP.md`.

## New design, no compatibility layer

Keeping WhatsApp running does not mean keeping its old shapes. The preparation
replaces contracts outright and ships no translation (PLAN.md, "no backward
compatibility"):

- There is no legacy phone selector. A caller uses the neutral peer reference
  or chat target; the adapter never validates digits or guesses a provider from
  the value.
- There is no retained phone output and no presentation projection for one. A
  generic API handler never queries SDK stores, infers phone data from opaque
  subjects, or adds an untyped attributes bag to preserve an old field.
- There is no telephone template reference and no alias for it. The retired
  `contact.*` root fails validation as an unknown root; a shared graph is never
  rewritten per adapter, and unsupported aliases never render empty (ADR
  0018/0020).
- A peer card is shared content, not a directory entry or binding identity.
  The telephone-shaped declaration/content model is replaced by a neutral
  card: a display name plus reachability entries (`phone`, `email`,
  `address`, `url` kinds as payload labels), each an opaque string the
  shared code never parses. Entry values may be templates; each adapter
  projects them onto its native card object and rejects the variants it
  cannot compose. WhatsApp composes and parses vCard 3.0 entirely inside
  its adapter. A future `invite` kind for connection links stays undecided;
  see [open-decisions.md](open-decisions.md).
- The retired telephone-shaped card YAML gets no compatibility decoder. Update
  validation, schema generation, publication, round-tripping, and stored
  content codecs together; the old shape simply fails to load.
- Public phone paths, templates, signed webhook envelopes, and CSV archives are
  separate contracts. A clean neutral format may break their old meaning
  freely; never rebuild already queued webhook payload bytes or weaken
  signature/retry guarantees. Development-database reset permission needs no
  additional approval to break API/YAML/template shapes.

## Evidence to close the cleanup

- [ ] P1 peer, inbound/history, and store identity contracts carry no
  provider-specific field, parameter, parsing, shared SQL column, or
  universal phone template value. Current consumers work; later
  setup/card tasks are explicitly pending.
- [ ] A nonnumeric sender and a different direct-thread ID survive
  inbound processing, API sends, external replies, history,
  holds/guards, and restart without creating a peer from the thread.
- [ ] Two store-local fakes both use local peer `7`; neither peers nor
  targets collide. Reconnect and a real close/reopen retain composition;
  replacing the store/profile cannot send the previous binding's pending
  entries when the P3/P5 gates close.
- [ ] WhatsApp's adapter still resolves PN/LID, restores its companion
  device after restart, enforces its ownership policy, pairs/unpairs,
  sends text/cards, and supplies approved legacy outputs without a
  provider-specific property in the final common contracts.
- [ ] Telegram-shaped bot/user fakes, a Signal-shaped opaque
  service-identity fake, and a fixed SimpleX-profile fake pass the same
  common path. No fake implements a phone getter or fabricates a phone
  to make account setup, peers, peer-first-seen events, or sending
  succeed.
- [ ] Card payloads round-trip through codecs; unsupported variants fail
  before queueing. A telephone entry in a card never creates a peer,
  selects a destination, or changes a binding.
- [ ] Compatibility tests cover each retained WhatsApp
  selector/template/result/archive shape and reject its use for other
  definitions. Neutral events/exports carry scoped identity and
  provenance.
- [ ] The P6 field/parameter/context/dependency audit passes. No
  universal phone, device, username, QR-only account model, provider
  switch, raw wire identity, or unchecked provider-payload map remains
  in shared production contracts. Provider examples/data do not count as
  common semantics.
- [ ] Register an independent synthetic fifth provider with unfamiliar
  opaque identifiers using only adapter/test files and one
  composition-root registration. It exercises existing capabilities
  without new core records, enum values, config union arms, SQL columns,
  API handlers, or engine branches. This demonstrates extension
  locality, not support for all imaginable future protocols.

## ADR alignment before implementation

The owner's cleanup steer is clear; concrete compatibility and
card-format changes still require their owning decisions. Do not claim
accepted ADRs already describe this direction:

- ADR 0051 and ADR 0029: P1 identity/target types, scoped store operations,
  neutral own identity versus adapter device lookup, optional explicit
  participant, and common phone removal are aligned as design.
- ADR 0020: scope PN/LID normalization and phone presentation to the
  WhatsApp boundary.
- ADR 0018: P1 per-bot template compatibility is selected; missing-path
  errors remain intact. ADR 0054 still owns other config/public choices.
- ADR 0036: neutral peer-card format and its capability/codec
  transition.
- ADR 0053, 0021, and 0039: neutral binding/setup/ownership/readiness
  versus the legacy WhatsApp result.
- ADR 0037/0042: P1 historical directory origin and migration compatibility
  are selected. ADR 0044/0045 and later phases still own selectors, exports,
  public read models, and first-seen envelope compatibility.
- ADR 0004 and 0034: inward dependency direction and consumer-owned
  ports. Preserve them while updating `glossary.md` and
  `architecture.md` for any new neutral domain vocabulary.

Update canonical ADRs in place before implementing each affected slice.
The P1 design alignment above does not implement or close a runtime gate.
