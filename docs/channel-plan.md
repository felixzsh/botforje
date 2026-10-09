# Messaging channel preparation plan

Status: P0 decisions accepted; P1 partial and reopened for identity cleanup; P2–P6 pending.
Code baseline: `3544357`, inspected on 2026-10-08. ADR 0051–0054 own the accepted decisions.
The completed documentation-only P0 is not part of the execution checklist below. Described
reference cases are not executable fixtures, and an accepted ADR is not a passing runtime gate.
`../PLAN.md` Phase 7 owns overall progress; this document defines the P1–P6 code outcomes and gates.
`../ROADMAP.md` owns the later implementation of actual additional channels.
Contract and vocabulary renames live only in [domain-contracts.md](domain-contracts.md).
The owner-approved contract review of 2026-10-09 is reflected in section 2.1 and the phase gates.
Provider-specific evidence lives in `channel-adapter-findings/`; it is not a capacity guarantee.

## 1. Outcome: adding a channel is adapter work

Prepare Botflota to operate through messaging channels without assuming any one provider.
WhatsApp becomes an ordinary registered adapter, not the runtime's implicit account model.
Later Signal, SimpleX Chat, or Telegram work should concentrate on the adapter, its protocol,
credentials, options, and capability declaration, rather than reopening the messaging architecture.

- Expected changes: its implementation and tests under `adapters/`, one composition-root
  registration, approved dependencies, and adapter-specific examples or guides. Generated schema
  changes are allowed when they follow the registered option types.
- Unexpected changes: transport ports, shared domain records, bot declarations, configuration,
  engine routing, fleet control, generic API/CLI handlers, or shared persistence schema/stores.
  Needing those changes to onboard an already-modeled channel means this preparation is incomplete.
- A genuinely new capability may justify a separate core extension. It must not be disguised as
  raw protocol JSON, arbitrary commands, or a growing provider switch in shared code.

The final gate must demonstrate this rule by registering an independent test adapter after the
shared runtime is in place. Merely implementing the messaging transport port is insufficient.

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

Keep the transport port centered on connect, disconnect, send, and inbound messages; preparation
may refine their shared values and errors without creating a universal interface for every feature.
Binding lifecycle and interactive attempts belong beside transport, not inside connection startup.
Reuse optional reporting, external-send observation, and history capabilities where they fit.
Do not add unused receipt/editing ports, runtime plugins, reflection-based registration, or `init`.
The core remains standard-library-only; `config` never imports adapters or `engine`.

Both operating paths must work: an operator using YAML and the CLI, and a host backend using the
scoped API for remote account setup and messaging. The host owns its callers and interface;
Botflota owns setup, credential custody, runtime, and scope authorization (ADR 0046).
Remote callers need no SSH, runtime file edits, or database access. Provider approval may still
require their messaging app. No host credential or reusable channel secret reaches the browser.

This plan implements architectural preparation, not new production adapters, provider SDKs,
a dashboard, cross-provider peer merging, shared-account dispatch, or infrastructure
provisioning.
No dependency is approved here. Group graph policy and new interactive DSL features remain separate
work; unsupported shared conversations must be refused, not treated as direct messages (ADR 0051).

### 2.1 Logical channels and adapter-owned resources

**Owner-approved direction (2026-10-09).** Keep the neutral identity, chat, binding, and graph-run
model. Clarify its behavior against direct clients, HTTP update sources, and shared auxiliary
services without making those implementation choices common domain objects.

- A channel is one bot's logical view of its committed binding, not ownership of one physical
  socket or process. Connect/disconnect start and stop that view; HTTP polling or webhooks need
  no persistent client connection. Releasing a view must not stop another binding's operations.
- Registered adapter assembly may own instance-lifetime shared resources and produce per-bot
  views. Coordination, physical event readers, and context selection stay adapter-owned and
  synchronized, never package-global state or common pool/process-management ports.
- A binding's verified identity and generation are independent of the auxiliary service's
  currently selected profile. Temporary context selection is not bind/unbind or replacement.
  Reject observations from a retired binding rather than assigning them to its replacement.
- Common persistence records the neutral association and delivery provenance. Protocol secrets
  and sessions remain adapter-owned, including a managed auxiliary store where appropriate;
  they need not be copied into Botflota's SQLite file. ADR 0053 still gates credential protection,
  and ADR 0054 still gates trusted endpoints and deployment ownership.
- Message IDs, source update IDs, and receive cursors are distinct. Deduplication uses the
  provider's actual message space and destination, including account context where required.
  Retained history must not suppress a different binding's new message merely because its ID
  repeats. Reauthorization/reconnect must not invent a new space to evade normal deduplication.
- An inbound handoff is not durable processing. P5 must settle the narrow completion boundary
  for committed processing, already-recorded duplicates, deliberate discards, and failures before
  claiming support for a source that confirms by cursor advancement or an HTTP response.
  The signature and failure/discard semantics remain TBD; no universal durable inbox is approved.
- Logical data isolation does not guarantee independent throughput or failure domains. A shared
  source can stall when one destination queue fills. Keep buffers bounded and do not silently
  drop accepted messages; explicitly settle replay, coupled backpressure, or justified buffering
  before deploying a shared source. No fairness guarantee, service grouping, or sharding policy
  follows from one actor per bot (ADR 0046).

P2–P6 prove these boundaries with small fakes. They do not implement a production SimpleX service
coordinator, choose a service supervisor, or promise a particular number of concurrent accounts.

### 2.2 Capability-aware behavior from authoring to delivery

[ADR 0052](adr/0052-declared-channel-capabilities.md) already requires one capability vocabulary
across configuration, execution, discovery, and delivery. The following makes its acceptance
criteria explicit; it does not introduce a new capability framework or approve new message kinds.

The domain stays channel-neutral, but a bot's usable behavior is bounded by its selected installed
adapter and account mode, not by a provider-wide feature list. Two bots on the same provider may
support different content, variants, operations, or limits. Declaring support is not proof of
account authorization or present conversation eligibility.

1. **Configuration:** validate every consuming bot's referenced graph and action steps, including
   entry/exit actions, tools, and guard fallbacks, against that bot's declaration. A shared action
   can be valid for one bot and invalid for another; guards or conditional paths do not excuse a
   statically unsupported step. Check known payload variants, options, and limits, not just the
   top-level message kind. CLI validation, startup, reload, and API publication use the same checks
   without opening a channel. A structurally valid editor schema is not proof of this compatibility.
2. **Execution and API admission:** validate resolved content and the actual target bot before
   accepting a send into the durable queue. Templates and runtime/API values can violate a limit
   that static configuration cannot determine. Reject at the earliest boundary with the required
   information; select any already-supported explicit fallback before dependent side effects.
   This is not a promise to predict future network state or undo earlier external HTTP effects.
3. **Discovery and delivery:** expose sanitized declared support per bot, separately from account
   authorization and conversation eligibility. Recheck dynamic eligibility immediately before
   delivery; an open reply window, recipient permission, or live account right cannot be certified
   by offline configuration validation. Unsupported/permanently invalid sends fail cleanly, without
   silent conversion, dropping, or exhausting retries for a feature that cannot be delivered.

Diagnostics identify the scoped bot, selected channel/mode, and unsupported capability or violated
limit. For definitions, include file/entity and graph/node/action/step context where applicable;
API errors remain scoped, actionable, and free of credentials or raw provider error details.

The [outbound research](channel-adapter-findings/outbound-messages.md) supplies contrast cases,
not the runtime's capability catalog. An unmodeled payload/interaction remains invalid under strict
decoding; platform support does not grant implicit adapter support. Each future typed capability
must join declaration, validation, execution/API admission, discovery, codecs/schema where relevant,
and conformance tests in its owning slice. Do not prebuild every researched feature now.

## 3. Execution rules

Deliver P1–P6 in order, preserving WhatsApp operation at every phase boundary. Each phase includes
the affected consumers, executable tests, and compatibility translations needed to keep the tree
working; do not leave public ports changed while their callers remain on the previous model.

- Check a task only when its code and tests exist; check a phase only when its gate passes.
- Update the corresponding `../PLAN.md` deliverable in the commit that completes the phase.
- Transitional WhatsApp translations belong at named compatibility boundaries, not throughout the
  engine, fleet, or stores. They must not become a second independent account/setup implementation.
- The owner requires a provider-neutral domain, not merely optional provider-specific fields.
  Section 5 defines the JID/phone cleanup and its phase boundaries; align its canonical ADRs before
  implementing the changed contracts. Existing partial code is not evidence that a task is closed.
- Use table-driven `t.Run` tests, hand-written fakes, and `testdata/` fixtures. Use fakes early,
  not only after designing every abstraction around the existing WhatsApp adapter.
- Follow repository build permissions and Make targets. Run checks in the main repository,
  never in a worktree; do not infer successful tests or manual verification from a design review.
- This is a map of architectural changes and acceptance criteria, not method-by-method instructions.
  Reuse existing code where it fits and avoid unrelated refactoring or speculative frameworks.

## 4. Phases and code checklist

### P1 — Neutral identity, conversations, and durable targets

Change: `message.go`, `records.go`, `ports.go`; `adapters/sqlite/sqlite.go` and its stores;
`engine/engine.go`, `engine/steps.go`, `engine/context.go`; `fleet/filter.go`, `fleet/fleet.go`,
`fleet/outbox.go`; WhatsApp account, channel, and ingest mapping. Adapt API/CLI/webhook consumers
as needed. Section 5 separates this identity cleanup from later setup and payload work.
Decision owner: ADR 0051, with ADR 0006, 0018, 0020, 0021, 0026, 0027, 0029, 0037, 0040, and 0043.

- [ ] Replace mandatory phone addressing with an identity key and a separate conversation target
  across inbound, outbound, external-reply observations, records, and store calls. Do not rename a
  phone column and use it as an undocumented container for unrelated provider IDs.
- [ ] Carry provider, subject, and conversation kind/thread/topic explicitly.
  A provider whose ids are local to one store or profile composes that stable space into its own
  subject and thread, so the core needs no second identity field. This closes key propagation only,
  not account metadata, binding generation, or delivery authorization.
- [ ] Remove JID and phone fields, parsing, and parameters from common messaging identities,
  inbound envelopes, store ports, and shared identity columns (section 5). Persist a neutral account
  subject and keep the device/session locator in adapter-owned storage. Do not hide a device locator
  under a generic identity name, add telephone metadata, or use an unchecked attributes map.
- [ ] Key registered identities by scoped provider identity and chats by target, not sender alone.
  Update identity/chat/directory SQL, indexes, constraints, and store signatures together.
  Keep sender identity distinct from thread identity; a group is not one person's identity.
- [ ] Replace the obsolete phone-only/identity-only uniqueness constraints and every dependent
  lookup with the neutral ones. The schema is edited in place under the pre-release reset policy
  (`../PLAN.md`), so the WhatsApp backfill and the pre-change database gate are gone; the runtime
  gates below still hold from a fresh creation.
- [ ] Resolve identities by subject and execution state, holds, and guards by chat. Make replies
  default to the inbound conversation, not the sender or the last conversation cached for that
  sender.
- [ ] Remove universal target-to-identity and identity-to-target derivation. Carry known participant
  identity explicitly; a valid target must not fabricate its unknown participant (section 5).
- [ ] Propagate only neutral common template values, including the internal identity ID. Remove
  telephone seeding and validation; preserve missing-path errors. Legacy WhatsApp references
  require a named compatibility translation, not an empty phone field for every channel.
- [ ] Persist the exact conversation target on outbound records and queued deliveries.
  Retain the triggering-chat history convention for alternate `to` while carrying the actual
  destination. This does not close binding snapshots or destination-provenance deduplication.
- [ ] Persist and test actual delivery provenance independently of the triggering history chat.
  Alternate-recipient provider IDs must deduplicate in their destination, not the source chat.
  Binding metadata/generation is implemented in P3 and enforced on queued delivery in P5; do not
  claim that those later requirements are already implemented here.
- [ ] Finish WhatsApp live events, outbound send, history, address-book import, and echo mapping
  to neutral values. Its direct-chat PN behavior remains compatible; shared conversations are
  explicitly filtered/refused until their runtime policy is settled. Include the neutral account
  subject/device lookup and removal of common phone metadata; partial mappings do not close it.

Gate:

- [ ] Prove opaque own/remote identity subjects and threads need no JID or phone field. Recheck
  provider/store/scope isolation and independent sessions, holds, and guards for one sender in two
  supported conversations, including direct conversations whose thread differs from the sender.
- [ ] Close the database before reopening it, create a fresh channel/fleet with no sender cache,
  and send to the persisted conversation. Cover alternate-recipient IDs overlapping source-chat
  IDs. P3/P5 separately prove committed binding identity/generation and replacement safety.

### P2 — Extensible definitions, configuration, and selective assembly

Change: `spec/bot.go`, `spec/action.go`; config loader/validation/schema/writer/reload and template
validation; `cmd/botflota/main.go`, `fleet_cmd.go`, `config_cmd.go`, and reload assembly;
fleet construction; the WhatsApp definition and its option translation.
For the card slice: `message_content.go`, its codecs, `engine/steps.go`, and WhatsApp card mappings.
Decision owner: ADR 0052 and 0054, with ADR 0007, 0009, 0018, 0034, and 0036.

- [ ] Introduce the immutable injected catalog of installed definitions. Distinguish provider,
  adapter kind, and account mode; select one unambiguous installed implementation. New definitions
  must not require adding provider enums or conditionals throughout shared packages.
- [ ] Add the accepted channel selector to bot declarations, preserving missing-selector WhatsApp
  compatibility. Reject explicit unknown/unavailable selections, never silently fall back.
- [ ] Make adapter-owned option types and their strict validation/schema declarations extensible
  through registration. Do not add a field or a provider-specific union arm to bot declarations for
  every adapter; do not replace typed runtime values with unchecked option maps.
- [ ] Separate common pacing/filter settings from adapter-specific capabilities and defaults.
  Preserve old WhatsApp YAML through its compatibility translation, without enabling WhatsApp's
  presence/history defaults on other channels or ignoring explicitly unsupported settings.
- [ ] Make template declarations and legacy selector/peer-card decoding follow the consuming
  bot's definition (section 5). Common `spec` types and generated schema must not gain universal
  phone fields; a shared action using a WhatsApp-only legacy alias cannot validate for another bot.
- [ ] Implement the agreed neutral peer-card declaration/content shape, stored codec, and existing
  WhatsApp send/ingest mapping in the same slice. Do not change declarations in P2 while
  leaving engine/content consumers broken until P5; preserve supported cards and declare limits.
- [ ] Inject the same pure declarations into CLI validation, startup, watcher loads, schema
  generation, and publication. Preserve origins, secret policy, round-tripping, logical versions,
  and atomic writes. Validation must not open SDK stores, start transports, or perform login.
- [ ] Validate each consuming bot's graph, entry/exit actions, tools, and guard `on_blocked` steps
  against its installed adapter. Declare implemented content/features and provider-correct limits,
  not a platform-wide promise; default to rejection instead of silent downgrade.
- [ ] Replace WhatsApp-only fleet construction with registered builders. Move settings translation
  and channel-specific dependency assembly into the adapter's integration boundary; initialize
  device stores or auxiliary resources only when the selected adapter/bind method needs them.
  Permit adapter-owned shared dependencies behind independent per-binding channel views;
  config-only operations still perform no channel I/O.
- [ ] Compare selectors, account modes, options, and binding-relevant references on reload.
  Preserve restart-required transport changes, saved/active differences, and isolation of peers.
  Config publication must not replace runtime credentials or silently migrate account identity.

Gate:

- [ ] Old YAML works; explicit unavailable selections and unknown option fields fail with context.
  The generated editor schema and runtime validator agree on installed option shapes.
- [ ] A preconfigured text-only fake joins a mixed fleet through registration and runs without
  WhatsApp devices, QR setup, or adapter-specific settings. Common construction has no provider
  switch, and config-only operations perform no channel I/O.
- [ ] A registered fake builds two logical channels over one shared resource without new common
  pool or process-management contracts. Starting/stopping one view leaves the other usable.
- [ ] Unsupported referenced actions/settings fail for the consuming bot; invalid publication
  preserves files and active channels, and a restart-required edit does not reconnect a peer scope.
- [ ] One shared action/graph is checked independently for a capable fake and a text-only fake;
  the incompatible bot fails with file/entity/action/step context even behind a guard or tool path.
  Cover a supported kind with an unsupported variant or known limit violation, and two definitions
  for the same provider with different account modes. Use existing modeled content, not a new DSL.

### P3 — Provider-neutral binding lifecycle and bind runtime

Change: `records.go`, `pairing.go`, lifecycle ports in `ports.go`; binding persistence;
`adapters/whatsapp/pair.go`, `pairing.go`, and account ownership; fleet/startup readiness and
assembly.
Decision owner: ADR 0053, with ADR 0021, 0022, 0025, 0028, and 0039.

- [ ] Complete P1's neutral account record with committed binding metadata, stable identity-space
  context where needed, and generation, separately from adapter credentials. Implement
  inspection, bind, and unbind operations; select by the bot's definition, never inspect every bot
  through the WhatsApp device store. Remove telephone-bearing results and phone-specific errors;
  keep legacy pairing results at the compatibility boundary (section 5).
  Separate common binding persistence from adapter/auxiliary credential stores; do not impose
  a shared SQLite credential schema on every implementation.
- [ ] Separate credential presence, provider authorization, transport health, and import progress.
  Readiness exposes safe actionable conditions; stored credentials or connection startup
  return do not prove authorization/connection. Missing transport reporting means unknown.
- [ ] Implement the accepted session/step model: scoped bot, session ID, revision, times, safe
  errors, display challenges, typed write-only inputs, external approval, and managed-profile
  selection. Distinguish login QR challenges from public invitations.
- [ ] Reuse per-bot mutation serialization and completion waits for begin/input/cancel/detach.
  Workers belong to the instance, not HTTP request lifetimes; expiry, retained sessions, retries,
  and shutdown are bounded. Active setup sessions remain in memory, not resumable secret storage.
- [ ] Stage setup until provider confirmation and local binding commit. Preserve the actual
  outcome across cancellation/expiry, report activation failures separately, and handle provider
  success followed by local persistence failure with cleanup or an explicit recovery condition.
- [ ] Adapt WhatsApp QR pairing and legacy results to the same lifecycle/session machinery,
  preserving its credential store, confidentiality, and successful-pairing/history-wait protection.
  Import completion must not define generic account success.
- [ ] Support same-identity reauthorization and explicit identity replacement through the same
  contracts. Preserve stable identity spaces, verify ownership, serialize with sends, and keep
  history. Detachment forgets the local binding; it does not destroy an account or managed profile.
  Retire the old binding's observations before a replacement starts; temporary backend context
  selection changes neither binding identity nor generation.

Gate:

- [ ] Executable fakes complete QR, credential-only, multi-step approval, and local-profile setup
  without phone fields or a universal QR. Reconnect/restart preserves committed identity spaces.
- [ ] Test stale input/session IDs, QR rotation versus session expiry, duplicate request keys,
  cancellation versus commit, detach during import, commit then activation failure, and shutdown.
  No stale/cancelled worker activates later, and successful setup never activates twice.
- [ ] An unconfigured or revoked bot remains inspectable/setup-capable without stopping peers;
  detachment keeps its chats, directory, history, and the actual outcome of any completed setup.
- [ ] Two fixed bindings share a fake backend: selecting one context leaves both identities and
  generations unchanged; detach/replacement cannot reassign stale events or destroy the peer's
  profile. Credential ownership can remain outside common binding storage.

### P4 — Common API and CLI, not provider-specific control planes

Change: `api/api.go`, account/pairing handlers, `views.go`, `params.go`, `send.go`, chat
control/sync,
history operations/export; `cmd/botflota/bot_cmd.go`, `chat_cmd.go`, `message_cmd.go`,
`address_book_cmd.go`, client views, and help; peer-first-seen webhook serialization.
Decision owner: ADR 0053 and 0054, with ADR 0042, 0044, 0045, and 0046.

- [ ] Wire neutral lifecycle/bind/capability dependencies into the API. Remove constructor
  requirements for WhatsApp pairing and unsupported live features; distinguish reading already
  stored history/address books from invoking a currently available channel operation.
- [ ] Implement scoped channel/binding/method discovery and bind begin/read/input/cancel operations.
  Align public routes and legacy translations in ADR 0053 before changing endpoints; scope and bot
  remain part of every operation's ownership (section 7).
- [ ] Expose declared support, account authorization, and conversation eligibility as separate
  facts. Unknown never implies permission; a disconnected bot keeps support without invented rights.
  Translate absent optional operations to explicit unsupported outcomes, not internal errors.
- [ ] Use neutral peer/chat/binding references for reads, controls, sync, and sends. Resolve
  chat IDs under scope and bot, never by ID alone. Retain approved WhatsApp selectors and old routes
  as explicit compatibility translations over the same operations, not alternate implementations.
  Legacy phone outputs come from a typed adapter-owned projection, never from a universal domain
  field or by inspecting the shape of a neutral subject. Neutral IDs and legacy selectors must be
  unambiguous (section 5).
- [ ] Replace API-wide digit validation with the selected adapter's target rules and common
  admission checks. Preserve atomic API sends, `client_id` reconciliation, chat-control semantics,
  and the default reply path without assuming arbitrary-address outreach is permitted.
- [ ] Make operational CLI commands consume neutral API views/selectors and common bind steps.
  Keep the locked offline operator path and existing WhatsApp commands compatible; adding a new
  adapter with existing step kinds must not require another CLI handler or provider form.
- [ ] Carry neutral identity/provenance through webhooks and CSV export, preserving existing
  compatible fields only at named legacy boundaries. Approve any incompatible envelope/archive
  change before coding; do not put a universal phone field back into the event or store contract.
  Enforce provider and identity-space compatibility between the two bots' bindings before history
  migration; never present unreadable local targets as valid for a replacement bot.
- [ ] Authorize before reading binding/session data; keep out-of-scope targets opaque (404).
  Enforce stale-session/step conflicts, request-key retry safety, bounded input bodies and budgets,
  authenticated non-cacheable challenges, and redaction across JSON, logs, errors, and exports.

Gate:

- [ ] A scoped HTTP client completes each fake setup mode and reauthorization without filesystem
  access, then reads a conversation, holds/releases it, and queues a text send through the same API.
- [ ] Personal CLI operation uses those operations while the instance runs, with no database
  fallback; existing WhatsApp pairing, query, and send callers remain compatible.
- [ ] Cross-scope reads/inputs/cancellation, stale IDs, unsupported methods, and lost-response
  retries obey the accepted errors. Submitted tokens/passwords/codes are never echoed or exported.
- [ ] Per-bot discovery and API admission agree with config validation on declared capabilities.
  Unsupported submitted content/operations are refused before queueing or invoking the absent
  operation. A disconnected bot retains its declaration without invented dynamic permission.

### P5 — Delivery, replay, and optional operations without protocol leakage

Change: transport/error contracts in `ports.go` and `message.go`; `fleet/outbox.go` and actor
lifecycle/control; turn/send/message/outbox/history stores; WhatsApp send/events/history/ingest.
Decision owner: ADR 0052, with ADR 0004, 0011, 0035, 0036, 0040, and 0044.

- [ ] Enforce classified send outcomes: permanent invalid/unsupported input fails immediately;
  transient failures retry within bounds and honor retry hints. Losing binding authorization
  stops that bot with its queue pending; replacing the binding fails its previous entries.
  Do not classify outcomes by parsing provider error text in shared code.
- [ ] Validate rendered content/targets before queue admission and recheck dynamic eligibility
  at delivery. Never silently split one durable message into multiple wire sends or downgrade
  content. Apply an already-supported explicit fallback before dependent side effects;
  this plan does not create a new fallback DSL.
- [ ] Check the stored binding generation at admission and immediately before sending. Serialize
  detach/replacement with in-flight delivery; old pending entries cannot inherit a new identity.
  Close the unbound-send decision below rather than guessing a future binding for queued messages.
- [ ] Make send success mean downstream acceptance, not recipient delivery/read confirmation.
  Preserve the existing status contract through documented translation, optional provider IDs,
  and at-least-once crash behavior; report uncertain timeout outcomes honestly.
- [ ] Make channel lifetime work independently of SDK assumptions: one inbound reader, concurrent
  serialized outbox sending, adapter-owned stream closure, bounded buffering, cancellation,
  partial-start cleanup, and bounded disconnect. Do not reuse stopped instances accidentally.
  This is a logical per-binding lifetime. A shared physical reader stays adapter-owned; releasing
  one view must not close peer views. Shared transport failure is not authorization revocation.
- [ ] Normalize history/backfill and external-reply observations to the same conversation/binding
  values. Keep wire anchors opaque; no phone requirement or unconditional optional-feature calls.
  Preserve inert imports, peer-first-seen rules, own-send echo correlation, and held inbound.
- [ ] Exercise upstream replay/cursor behavior against processing success, failure, filtering,
  and suppression. Inbound handoff is not proof of durable commit; settle any required
  processing acknowledgement before advancing irreversible cursors. Do not add a universal
  durable inbox without its decision, or claim replay guarantees a channel cannot supply.
  Close the completion contract in ADR 0004/0040 before the P5 gate: distinguish confirmed
  processing, persisted duplicates, deliberate discards, and failures. Keep source update IDs
  and receive cursors out of message identity; do not advance confirmation on handoff alone.
- [ ] Verify message-ID space across binding replacement and retained/migrated history. Preserve
  destination and any required account provenance independently of the triggering history chat.
  Settle the durable key in ADR 0051/0026 before choosing SQL constraints; binding generation
  alone is not proof of the provider's message-ID namespace.
- [ ] Exercise bounded routing from a shared source to a slow or stopped logical consumer.
  Document the coupled backpressure/replay boundary rather than promising independent progress
  or adding unapproved durable buffering. Deployment policy remains TBD in section 7.
- [ ] Preserve typed content and stored codecs. Keep selections, message IDs, callback/event IDs,
  and control traffic conceptually distinct; unsupported interactions must not masquerade as text
  turns. Test the extension boundary without shipping unused button/editing/receipt machinery.
- [ ] Apply admission/delivery capability checks to P2's neutral peer-card payload. No common
  telephone parser or provider-specific payload map; preserve supported WhatsApp cards and reject
  unsupported card variants before queue admission.

Gate:

- [ ] Permanent failure does not block FIFO through useless retries; transient sends retry,
  account authorization loss is visible, and a replacement account sends none of the old queue.
- [ ] Test restart around send/recording, reused message IDs in separate destinations, own-send
  echoes versus human replies, held inbound recording, and channels without optional operations.
- [ ] Slow/cancelled producers and partially started adapters exit without leaks or dropped
  messages from full queues. Tests establish the actual acknowledgement/replay boundary, not an
  inferred exactly-once guarantee or recipient-delivery claim.
- [ ] A replay-capable fake confirms only after the agreed processing outcome; failed persistence
  remains replayable, and recorded duplicates and deliberate discards follow their explicit rules.
  Shared-source tests cover view teardown, context-selection races, and bounded backpressure.
- [ ] A statically valid template whose resolved payload violates the consuming bot's declared
  limits fails before queue admission, as does equivalent API input. Delivery rechecks a dynamic
  eligibility change separately from declared support; unsupported queued content fails without
  silent downgrade or useless retries. No test assumes earlier external effects can be rolled back.

### P6 — Prove extension locality and close regression coverage

Change: cross-package contract/regression tests and fixtures, plus the remaining documentation
and generated reference that must describe the implemented runtime rather than the accepted target.

- [ ] Run the same fixture graph through WhatsApp's test mapping and contrasting registered fakes:
  an opaque-ID text-only channel and a profile-local channel. Both traverse actual
  engine, stores, fleet/outbox, and API paths, not bespoke test-only execution pipelines.
  Include two bindings sharing one fake backend and an HTTP-style update source with processing
  confirmation. These are behavioral contrast cases, not new provider branches or SDK dependencies.
- [ ] Add an independent test adapter using only its adapter files and one registry entry. Prove
  setup with existing steps, strict option validation/schema, discovery, conversation reads,
  filters/holds, durable sends, restart, and detach work without editing shared production code.
  A required port/config/API/store change fails the extension-locality gate; fix the abstraction
  instead of adding another exception. Publish this reusable conformance test path for adapters.
  Include the capability-consistency gates from section 2.2 and P2/P4/P5 so future content/operation
  extensions cannot bypass config checks, runtime/API admission, or per-bot discovery.
- [ ] Review shared dependencies and hot paths: no SDK/wire identities, phone-only routing, QR-only
  readiness, unconditional WhatsApp stores, or provider switches outside named compatibility and
  registration boundaries. Apply section 5's stricter field/parameter/context audit: optional
  phone fields also fail the clean-domain gate. Keep transport, setup, support, and storage
  separate.
- [ ] Complete automated fresh-schema/reopen, scope-isolation, publication/reload, setup-race,
  retry, cancellation, and redaction coverage from the earlier gates. Fakes declare unsupported
  features honestly and require no new SDK, network service, or production credential.
- [ ] Update `glossary.md`, `architecture.md`, `api.md`, `integration.md`, relevant guides/help,
  and config examples to match the code. Regenerate the schema from implemented Go types.
  Do not repeat the completed ADR-writing phase; amend an ADR only for a real unresolved decision.
- [ ] Run `make check` and `make test-race` in the main repository with required permission, and
  complete the WhatsApp manual checklist. Report manual checks as pending until actually exercised.

Gate:

- [ ] The extension-locality demonstration and every automated gate pass; WhatsApp pairing,
  reconnect, live messages, queued replies, history import/backfill, and unpair remain operational.
- [ ] P1–P6 code outcomes are complete and tracked in `../PLAN.md`. Adding an adapter for existing
  capabilities now means adapter work plus registration, not another runtime architecture project.

## 5. Clean domain: removing JID and phone, not renaming them

**Owner direction (2026-10-08).** Binding, peer, chat, and execution contracts must not
contain messaging-provider-specific concepts. A phone is not a universal messaging identity;
making it optional or filling it with an empty string does not make the domain neutral. This
replaces the earlier plan to retain phone metadata in common records and template context.
The target is a provider-agnostic domain with clean/hexagonal dependency boundaries, not a
four-provider union or a renamed WhatsApp model. Future providers may have identities and flows
none of today's four services use; they must not inherit assumptions from those examples.

This section is an implementation plan, not a claim that the code or canonical ADRs already match
it. P1 removes identity leakage; P2–P5 remove the remaining config, setup, presentation, and payload
assumptions. Only P6 can claim that the entire shared domain is clean.

### 5.1 What belongs in the common contract

- Domain language follows the runtime's responsibilities, not a provider's API nouns. Renaming a
  field without removing its provider-specific meaning does not make the boundary neutral.
  Keep the exact naming inventory in the referenced catalog, not a second dictionary in this plan.
- Dependencies point inward: the core owns the contracts its consumers need; adapters implement
  them, and the composition root supplies implementations. The core never imports SDKs, adapter
  packages, SQL/YAML representations, protocol commands, or transport-specific lifecycle rules.
  Common engine/fleet/API code consumes ports, not provider stores or adapter implementation types.
- Shared persistence encodes the domain contract; an SDK schema or wire object does not define
  that contract. Config validation consumes injected pure declarations, not a live provider
  connection. Setup, delivery, persistence, presentation, and interactive challenges stay separate.
- Provider/adapter identifiers are open catalog values, not a closed enum of today's services or
  conditional branches in generic code. A catalog entry names an implementation, not a domain
  variant with its own optional fields. Keep ports small and owned by actual consuming operations.
- Model the invariants the runtime needs, not every feature a hypothetical provider might offer.
  A new provider fitting existing capabilities should need adapter work plus registration only.
  A genuinely new behavior may require an explicit neutral domain extension with its first real
  consumer; that does not justify a raw-command escape hatch or speculative universal framework.
- Authorization associates a scoped bot with a verified external identity, not a companion device,
  credential, endpoint, challenge, or display label. Association, authorization, transport health,
  and a temporary interactive attempt have separate lifetimes; no paired/unpaired boolean replaces
  those facts. Import completion must not be mistaken for successful binding.
- Identity and destination are distinct opaque values, normalized by their adapter. The core may
  compare and persist them, not parse protocol syntax or infer one from the other.
- The scope's identity registry and a binding-owned directory are different relationships. Receiving
  a message can register an identity without adding directory membership. Importing a directory or
  sharing a card does not invent chats, execution state, or first-live-inbound activity. Directory
  membership never grants send permission and does not prove current reachability.
- P1's minimum account record stores scope, bot, provider, and a normalized opaque account subject.
  P3 completes it with the selected adapter/mode, any required stable compatibility context, and
  generation from ADR 0051. Do not add empty placeholder fields merely to anticipate P3.
- A device/session lookup belongs to the adapter. Reuse its existing SDK store; add a persistent
  bot-to-device relation only if no existing link can recover it safely after restart. A renamed
  device locator is still not a verified account identity.
- Local IDs must include their durable identity space in adapter-normalized subjects and threads.
  Use documented, unambiguous composition; never derive the prefix from a bot name, port, path,
  active profile, connection, or reconnect counter. Do not restore separate namespace fields on
  normalized identity/target keys. Binding compatibility context is a separate P3 concern.
- Sender, participant, conversation, and account are distinct roles. A direct thread does not
  universally identify its participant. Use the supplied sender/participant or an adapter mapping;
  never derive a participant identity from the thread in generic code. Keep unknown identities
  unknown. Durable history, external addressing, and bounded execution have different lifetimes.
- Display names are optional presentation, not identity. Telephone, username, profile, and device
  properties do not become universal fields, including in read models. Adapters normalize names;
  their source-specific name provenance and precedence remain outside generic storage/runtime.
  Shared records, ports, read projections, and SQL columns carry normalized presentation only.
  Scope identity presentation and binding-directory presentation may differ; normalization is per
  context, not a fixed mapping of one provider name source to each domain field.
  Do not substitute unchecked metadata maps, protocol JSON, or a provider-specific union.
- A normalized WhatsApp subject may contain phone digits. That is an opaque value, not a promise
  that all subjects are telephone numbers. No generic digit validation, dial rules, suffix
  stripping, phone fallback, or subject-to-thread conversion follows from it.
- Typed message content remains neutral. Telephone data inside user text or a standard peer
  card is payload data, never a peer/binding key or an implicit permission to send.

### 5.2 Four provider cases that constrain the abstraction

These are contrasting examples, not an exhaustive taxonomy or the source of a closed domain
vocabulary. Do not design four parallel record variants or require every channel to supply the
union of their features. The common roles and declared capabilities are the contract.

**WhatsApp.** Preserve the existing PN normalization policy inside its adapter (ADR 0020), without
promoting it to a domain phone contract. A device JID locates native companion credentials;
it is not the account subject used for ownership. Pairing challenges, PN/LID resolution, device
suffixes, and wire addresses stay adapter-owned. Re-pairing the same verified account must not
create a new peer space just because its companion device changed. Other WhatsApp account modes
must supply their own verified mapping; do not presume a future Cloud API identifier is a native
device JID. Keep actual phone presentation only in its compatibility projection.

**Telegram.** For Bot API setup, verify the token and derive account identity from the returned
bot/user ID, not the token, its hash, or a mutable username [T1]. Sender user IDs, chat IDs, topics,
and chat-local message IDs have separate roles even when two numbers happen to match. Store their
normalized forms as strings; common records do not parse Telegram integer widths or signs. User
login is a separate account mode: a phone/code/password can be a setup input without becoming a
common account property. Bot API authorization is not arbitrary-recipient outreach permission.
Business connections and user sessions require their own adapter mapping, not another core field.

**Signal.** Phone registration or lookup does not mean peers always expose a number. Signal
supports phone-number privacy and mutable usernames used to initiate conversations [G1]. Its service
identifiers distinguish ACI and PNI [G2]; never collapse typed identifiers into a bare UUID or
infer a person from a phone/username without verified adapter-side mapping. A real adapter must
verify the supported account/service identity and normalize it opaquely; no ACI/PNI enum, phone
field, or device suffix belongs in the core. Setup, account recovery, number changes, and transport
support still need investigation when that adapter starts; these facts are not an SDK commitment.

**SimpleX Chat.** Bind one bot to one fixed, verified local profile in a managed chat store. A
WebSocket reaches the instance; the adapter must enforce profile ownership rather than assume the
connection is profile-restricted. In the inspected SQLite schema, `contact_id` is a database-wide
primary key with a `user_id` owner, not a separate contact counter per profile [S6]. Distinct stores
can both contain contact `7`; normalize them as different opaque subjects, for example
`store-a/contact/7` and `store-b/contact/7`, and apply equivalent protection to thread targets.
Preserve the store marker across reconnect/restart and verify the bound profile before sending;
an unrelated store at the same path is not the old identity. Invitation addresses are neither
global person IDs nor login credentials. A fixed-profile reply bot does not require automatic
profile switching in the common runtime or pretending to link an existing mobile account.
If several fixed bindings share a service, its adapter may need serialized context selection and
event demultiplexing. Section 2.1 defines the logical boundaries; concrete service coordination,
deployment, and capacity stay future work in `channel-adapter-findings/simplex.md`.

These cases need no provider enum, provider-specific field, universal credential table, or SDK
dependency in the domain. Fakes represent their differing identity semantics; real adapters remain
future work in `../ROADMAP.md`.

### 5.3 Concrete removal work and phase ownership

**P1 — identity and its existing consumers, one coherent slice.**

- Reconcile the incomplete account-record and SQLite identity edits with their callers/stores
  before continuing. They are unfinished, not an accepted implementation. End with a normalized
  external subject, no common phone field, adapter-owned device
  recovery, and consistent SQL, reads, writes, ownership checks, and tests.
- Apply the catalog's identity, directory, chat, execution, field, and helper changes coherently.
  Remove common telephone properties and inbound-store parameters. Change root contracts, SQLite
  identity/chat/directory/history stores, and WhatsApp ingest mappings together.
- Keep only neutral identity columns in shared tables. Phone presentation and device addressing,
  if still needed by a supported compatibility operation, are adapter-owned data/projections,
  not optional columns in shared identity or binding tables.
- Remove generic target/participant derivation and update engine turn/load,
  external-reply recording, send admission, and `api/params.go`. Reuse existing explicit recipient
  values where correct; when only a target is known, do not require or invent recipient identity.
  Preserve atomic send/turn behavior and the current explicit refusal of shared graph execution.
- Stop seeding telephone data in `engine/engine.go`; expose neutral identity/name values and
  declare any needed neutral subject/provider paths explicitly. An internal identity ID is not a
  provider address; a mechanical template substitution is not a safe migration.
  Adapt existing template consumers through section 5.4, without weakening missing-path errors.
- Adapt API, CLI, webhook, and export consumers enough to keep this slice runnable. Remove their
  dependency on deleted identity fields rather than postponing a broken caller to P4. P4 later
  completes the common public representations and compatibility discovery.

P1 does not implement interactive bind attempts, full binding lifecycle/generation, or all content
changes. Those tasks stay unchecked under their owning phases; partial identity cleanup is not a
claim that binding and peer-card contracts are already provider-neutral.

**P2 — definitions, templates, and declarative cards.** Put compatibility decoding and template
declarations in the selected definition. Common `spec` objects must not describe `to` as a phone,
assume a peer subject is a chat target, or acquire a phone option for every provider.
Validate each consuming bot separately; a shared graph/action is not automatically portable.
When section 5.4's neutral card shape is agreed, update `spec/action.go`, config validation,
writer/template checks, root typed content/codecs, engine steps, and existing WhatsApp mappings
together. Regenerate the schema; do not hand-edit generated JSON or defer broken card callers to P5.

**P3 — binding persistence and lifecycle.** Complete neutral association metadata and generation;
replace telephone-bearing results, pairing-only readiness, and phone-specific error semantics with
neutral outcomes. A provider-required identifier can be supplied during bind, but remains a typed,
protected step input, not a persistent account property or reusable credential. Preserve WhatsApp's
ownership rule inside its implementation; do not impose
one-phone/one-device semantics on all modes. Compare verified account identity and compatibility
context on renewal/replacement; a changed display name or refreshed token is not a new account.

**P4 — neutral public views and explicit compatibility.** Remove universal phone properties from
new peer/binding/chat views, common CLI models, event payloads, and CSV schemas. Replace
phone-only peer-first-seen serialization in `webhook/webhook.go` with neutral peer identity and
scope/bot provenance. Preserve existing consumers only through the approved named translation;
resolve neutral IDs under their ownership and prevent ambiguity with old numeric phone selectors.
History migration checks the source/destination bindings and adapter compatibility, never parses
opaque prefixes or infers compatibility from two matching phones.

**P5 — delivery and optional-content eligibility.** Validate/normalize targets before admission and
check their committed binding generation before delivery. A valid ID or phone-like string does
not establish reachability or permission. Apply the common admission/delivery checks to P2's card
payload and its declared limits/variants; no phone lookup belongs in fleet pacing, deduplication,
replay, or content routing. P2 already owns the coherent payload/spec/codec/WhatsApp mapping change.

**P6 — full boundary audit.** Check root contracts, `spec`, common config, engine, fleet, shared
stores, generic API/CLI/event models, template context, errors, and comments for provider-specific
semantics. Protocol code/data is permitted only in adapters and named compatibility boundaries.
Keep the existing typed card capability; removing an identity leak must not silently
delete peer-card support or reinterpret a card as a chat peer.

### 5.4 Compatibility and peer cards without polluting the domain

Keeping WhatsApp operation does not require keeping its phone fields in common objects. Separate
legacy input/output formats from the runtime contracts they translate to:

- A legacy phone selector is accepted only where WhatsApp compatibility is explicitly selected.
  The boundary normalizes it to a peer identity or chat target via the adapter's rules. The neutral
  operation never validates digits or guesses a provider from the value.
- Existing phone outputs, if retained, come from a bounded typed WhatsApp presentation projection.
  Do not make generic API handlers query SDK stores, infer phone data from opaque subjects, or add
  an extensible untyped attributes bag merely to preserve one old field.
- A telephone template reference is not a universal optional path. Translate old WhatsApp references
  to declared neutral paths before engine assembly where semantics are equivalent; otherwise use
  a narrow adapter-owned compatibility projection. Choose and record the actual translation before
  coding. Apply it per consuming bot, never by mutating a shared graph for every channel or by
  textual search-and-replace. Unsupported aliases fail validation, not render empty silently.
- A peer card is shared content, not a directory entry or binding identity. The current
  telephone-shaped declaration/content model is included in the cleanup, not exempted as optional.
- Recommended minimal replacement: a named card carrying bounded standard vCard content, with no
  core telephone property or provider parser. Telephone entries may exist inside the standard
  payload, like numbers in a text message; they do not participate in identity or routing. Agree
  the supported vCard version/subset, templating, limits, and validation in ADR 0036 before coding.
- WhatsApp maps that payload to its native card. Telegram's `sendContact` still requires a phone
  and first name [T1]; its adapter must extract/validate supported card data or reject that variant,
  not force those fields into every domain object. Signal/SimpleX advertise card support only when
  their actual implementations supply it. Do not build a universal peer-management subsystem.
- If old telephone-based card YAML remains supported, its adapter-owned compatibility decoder
  constructs the neutral card before execution. Update validation, schema generation, publication,
  round-tripping, and stored content codecs consistently; compatibility must not reintroduce
  a telephone property into common declarations.
- Public phone paths, templates, signed webhook envelopes, and CSV archives are separate contracts.
  Approve migration/versioning where a clean neutral format cannot preserve their old meaning.
  Never rebuild already queued webhook payload bytes or weaken signature/retry guarantees.
  Development-database reset permission does not authorize silent API/YAML/template breakage.

### 5.5 Evidence required to close the cleanup

- [ ] P1 peer, inbound/history, and store identity contracts have no JID/phone field,
  parameter, parsing, shared SQL column, or universal phone template value. Current consumers work;
  later setup/card tasks are explicitly pending.
- [ ] A nonnumeric sender and a different direct-thread ID survive inbound processing, API sends,
  external replies, history, holds/guards, and restart without creating a peer from the thread.
- [ ] Two store-local fakes both use local peer `7`; neither peers nor targets collide.
  Reconnect and a real close/reopen retain composition; replacing the store/profile cannot send
  the previous binding's pending entries when the P3/P5 gates close.
- [ ] WhatsApp's adapter still resolves PN/LID, restores its companion device after restart,
  enforces its ownership policy, pairs/unpairs, sends text/cards, and supplies approved legacy
  outputs without a JID/Phone property in the final common contracts.
- [ ] Telegram-shaped bot/user fakes, a Signal-shaped opaque service-identity fake, and a fixed
  SimpleX-profile fake pass the same common path. No fake implements a phone getter or fabricates
  a phone to make account setup, peers, peer-first-seen events, or sending succeed.
- [ ] Card payloads round-trip through codecs; unsupported variants fail before queueing.
  A telephone entry in a card never creates a peer, selects a destination, or changes a binding.
- [ ] Compatibility tests cover each retained WhatsApp selector/template/result/archive shape and
  reject its use for other definitions. Neutral events/exports carry scoped identity and provenance.
- [ ] The P6 field/parameter/context/dependency audit passes. No universal phone, device, username,
  QR-only account model, provider switch, raw wire identity, or unchecked provider-payload map
  remains in shared production contracts. Provider examples/data do not count as common semantics.
- [ ] Register an independent synthetic fifth provider with unfamiliar opaque identifiers using
  only adapter/test files and one composition-root registration. It exercises existing capabilities
  without new core records, enum values, config union arms, SQL columns, API handlers, or engine
  branches. This demonstrates extension locality, not support for all imaginable future protocols.

### 5.6 Canonical decisions that must be aligned before implementation

The owner's clean-domain requirement is clear; concrete compatibility and card-format changes
still require their owning decisions. Do not claim accepted ADRs already describe this plan:

- ADR 0051 and ADR 0029: remove the common optional phone property, define the neutral account
  subject versus adapter device lookup, and keep sender/participant separate from thread identity.
- ADR 0020: scope PN/LID normalization and phone presentation to the WhatsApp boundary; stop
  describing telephone templates as common domain values or sessions as phone-keyed.
- ADR 0018 and ADR 0054: record the legacy template/config translation and any deliberate breaking
  change. Missing-path errors remain intact; nonphone channels get no empty universal alias.
- ADR 0036: settle the neutral peer-card format and its capability/codec transition.
- ADR 0053, 0021, and 0039: neutral binding/setup/ownership/readiness versus the legacy WhatsApp
  result. Identity-space compatibility and generation remain meaningful, not renamed phone rules.
- ADR 0037, 0042, 0044, and 0045: align address-book/read models, migration compatibility,
  selectors, exports, and peer-first-seen envelopes without fields returning to common identities.
- ADR 0004 and 0034 own inward dependency direction and consumer-owned ports. Preserve them while
  updating `glossary.md` and `architecture.md` for any new neutral domain vocabulary; renaming a
  protocol concept without moving its semantics back to the adapter does not satisfy these rules.

Update canonical ADRs in place under repository rules when implementing the affected slice. This
plan-only correction does not modify ADRs, code, schemas, or fixtures and does not close any gate.

## 6. Contract rename reference

[domain-contracts.md](domain-contracts.md) is the compact current-to-target naming catalog.
Keep contract, field, helper, and command renames there only; this plan owns architectural reasons,
phase outcomes, compatibility, and pending behavioral decisions. Naming a replacement does not
implement it, close a runtime gate, or authorize an incompatible public representation.

## 7. Remaining decisions: close only the affected boundary

The completed ADR work is not a new implementation phase. These operational gaps must still be
resolved with the user and recorded in their owners before incompatible behavior is coded:

- **P1, addressing/dedup:** key shapes and default reply destination are settled — identities use
  `(scope, provider, subject)`, and conversations use
  `(scope, bot, provider, kind, thread, topic)`, and an alternate `to` keeps the
  triggering chat's history while its queue entry carries the destination's own
  conversation. A store-local provider composes its space into the subject and
  thread. Section 5's account/phone cleanup, participant mapping, and real
  destination-dedup/restart gates remain unchecked. P3 implements committed
  binding metadata/generation; P5 checks the generation on delivery and closes
  ADR 0044's unbound-send decision below. These are not completed P1 requirements.
  Determine the message-ID space separately: account-local IDs must not collide with retained
  history after replacement. The normalized provenance and durable key are still to be settled
  in ADR 0051/0026, not assumed from a chat ID or binding generation.
- **P2, selection:** ADR 0054's provider/mode selector must resolve the adapter kind unambiguously.
  Decide how indistinguishable installed implementations are rejected or selected before supporting
  them; do not invent precedence or turn adapter kind into an account mode.
- **P1/P3/P4, directory provenance:** the scope-wide identity registry and binding-owned directory
  must not be conflated. Current history migration transfers directory rows to another bot. Decide
  retention/reimport/mapping after unbind, identity replacement, and history migration before
  changing ownership or adding references to a binding record that unbind deletes. Imported or moved
  directory data is not evidence of current membership or permission (ADR 0037/0042/0051).
- **P3/P5, status and generations:** decide which confirmed facts are durable and which observations
  are live or stale; do not persist yesterday's successful pairing as current readiness. Request
  revision, config reload generation, and binding generation are separate. Specify how association
  provenance survives delete/rebind for directories and queued sends (ADR 0051/0053/0040).
- **P2/P4, public compatibility:** select neutral HTTP resources and the translation/versioning of
  old commands, paths, template roots, filter/card keys, result fields, event topics, and archive
  sections. The rename catalog owns names, not permission to change serialized meaning. A first
  allowed inbound is not directory insertion; keep its durable timing and signed retry bytes intact
  (ADR 0018/0044/0045/0053/0054). Do not add a parallel lifecycle facade under the old vocabulary.
- **P5, history and external observations:** source retrieval, traversal/exhaustion, and persisted
  import outcome must remain distinct; no primary-phone request or call-anchor rule is universal.
  An emission outside this runtime is not necessarily human-originated. Settle normalized source
  and provenance boundaries with actual consumers before claiming support (ADR 0035/0043/0052).
- **P4/P6, persistence/export boundary:** domain history grouping must not mirror SQL table names.
  Keep physical schema, archive filenames, and legacy formats in their adapters; approve format
  changes independently of Go renames. A neutral card format also remains gated by section 5.4.
- **P5, unbound sends:** ADR 0044 currently accepts sends to an unpaired bot, but the new queue must
  be tied to the account authorized at acceptance. Decide whether no-binding admission is rejected
  or has another explicit safe contract; it cannot automatically attach to a later identity.
- **P5, receive commit boundary:** if a replay/cursor channel needs a durable-processing signal,
  settle the narrow acknowledgement contract in ADR 0004/0040 before closing P5.
  Do not silently promote a channel handoff into a durable inbox or a committed turn.
  Exact signatures, deliberate-discard rules, and failed-processing behavior remain TBD;
  distinguish a provider update ID/cursor from a persisted message ID.
- **Future shared adapters, deployment/backpressure:** decide trusted service ownership, grouping,
  shutdown, exclusive command coordination, and the supported overload/replay boundary before
  deploying a shared source. A logical view does not imply an independent physical failure domain.
  No supervisor, per-profile durable spool, fairness promise, or 1000-account capacity is approved
  (ADR 0046/0054). The preparation uses fakes, not a production service manager.

Credential encryption/key custody before storing a new real credential type (ADR 0053), and trusted
auxiliary endpoint/profile deployment (ADR 0054), remain gates for the corresponding future adapter.
They do not require implementing new providers to complete this preparation. Fakes use synthetic
inputs; a scoped caller must never choose arbitrary server paths, databases, or unmanaged endpoints.
Do not assume ADR 0047–0049's file-root, egress, or secret-catalog protections are implemented.

## 8. Reference cases, not adapter implementation instructions

Use these differences to challenge the contracts and fakes, not to select SDKs or promise support:

- WhatsApp: PN/LID resolution and device JIDs stay inside its adapter; QR setup, presence, history,
  and address-book import remain implemented features, not obligations for other channels.
- Telegram Bot API: write-only bot-token setup, separate user/chat IDs, conversation-local message
  IDs, and dynamic eligibility [T1]. A token is not login to a person's account or permission for
  arbitrary outreach. Connected accounts and multi-step user login are separate modes [T2–T5];
  shared-service dispatch is not implied by registering an adapter.
- SimpleX Chat: store-local identities/targets under a fixed profile, managed auxiliary transport,
  connection invitations distinct from login QR, and asynchronous send acceptance [S1–S4]. Do not
  infer an independent server login to an existing mobile profile from desktop linking [S5].
- Signal: phone privacy, mutable discovery usernames, and typed service identities constrain the
  common model [G1–G2]. Its actual setup/transport adapter still needs supported-flow investigation;
  no phone-based identity or universal QR follows from the existing WhatsApp implementation.

The required baseline is authorized direct text messaging plus usable account setup/binding.
Media, polls, formatting, buttons, groups/topics, receipts, history, address books, and external
reply observation are separately declared optional capabilities. A new feature is implemented with
its first real consumer; a provider's platform support does not automatically become adapter
support.

## References

Provider references were inspected on 2026-10-07; recheck them when a real adapter starts.
The SimpleX `stable` documentation is mutable, not a pinned SDK guarantee.
The identity correction in section 5 rechecked Telegram, Signal, and the pinned SimpleX schema on
2026-10-08. These sources constrain the model; they do not promise adapter support.

- [T1] [Telegram Bot API](https://core.telegram.org/bots/api).
- [T2] [Connected business bots](https://core.telegram.org/api/bots/connected-business-bots).
- [T3] [Telegram user authorization](https://core.telegram.org/api/auth).
- [T4] [Telegram QR login](https://core.telegram.org/api/qr-login).
- [T5] [Telegram 2FA](https://core.telegram.org/api/srp).
- [S1] [SimpleX bot API](https://github.com/simplex-chat/simplex-chat/blob/stable/bots/README.md).
- [S2] [SimpleX commands][simplex-commands].
- [S3] [SimpleX types][simplex-types].
- [S4] [SimpleX events][simplex-events].
- [S5] [SimpleX FAQ](https://github.com/simplex-chat/simplex-chat/blob/stable/docs/FAQ.md).
- [S6] [Inspected SimpleX source][simplex-identity-source], schema at
  `src/Simplex/Chat/Store/SQLite/Migrations/chat_schema.sql` (`contacts` and `users`).
- [G1] [Signal phone privacy and usernames][signal-phone-privacy].
- [G2] [Signal service ID implementation][signal-identity-source], at
  `java/shared/java/org/signal/libsignal/protocol/ServiceId.java`.

[simplex-commands]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/COMMANDS.md
[simplex-types]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/TYPES.md
[simplex-events]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/EVENTS.md
[simplex-identity-source]:
  https://github.com/simplex-chat/simplex-chat/tree/479548ee53ffb73db73841e77acbeee5a78dbbd5
[signal-identity-source]: https://github.com/signalapp/libsignal
[signal-phone-privacy]: https://signal.org/blog/phone-number-privacy-usernames/
