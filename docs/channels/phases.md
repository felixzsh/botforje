# Channel preparation phases

Status: draft, not approved for implementation. These checklists
are the execution half of [plan.md](plan.md); they inherit its
provisional status. Anything here may change as new entries land in
`provider-findings/` and `provider-differences/`. Claims that looked
closed but depend on that evidence were moved to
[open-decisions.md](open-decisions.md). Cleanup rationale lives in
[domain-cleanup.md](domain-cleanup.md); vocabulary lives in
[domain-terminology.md](domain-terminology.md).

`../../PLAN.md` Phase 7 owns overall progress; ADR 0051–0054 own the
accepted decisions. A described reference case is not an executable
fixture, and an accepted ADR is not a passing runtime gate.

## P1 — Neutral identity, conversations, and durable targets

### Implemented contracts and remaining boundaries

The bounded contract/rule design closed on 2026-10-11 and its implementation
landed on 2026-10-11. ADR 0051 defines comparable identity/target types, required
values, canonical absent topic, explicit optional participants, scoped store
operations, normalized own account identity, and adapter-owned device lookup.
ADR 0026/0029 separate chat history, graph execution, and first allowed
live-inbound activity.

Two policies were explicitly selected:

- Directory snapshots preserve source account identity when compatible history
  moves; data ownership is not current membership. Different origins remain
  independent snapshots (ADR 0037/0042).
- The retired `contact.id/name/phone` templates have no translation: the neutral
  `peer` root is the only one, with no universal phone field, shared-graph
  mutation, or silently empty unsupported aliases (ADR 0018/0020).

Durable local-space normalization follows ADR 0051; a provider match alone does
not prove migration compatibility. P3 association generations/freshness stay
outside this design closure, and the P5 receive/message-space decisions closed
on 2026-10-17 (ADR 0004, ADR 0026/0051). The durable message-ID/dedup key is
scoped by the owning bot and by the destination in P1, and by the
dapter-declared message space in P5.

P1 retires the `contact` root and propagates the neutral `peer` values. No
per-bot translation is added for it (owner decision 2026-10-12): the retired
references now fail as an unknown root instead of rendering an empty phone field,
and an adapter carries no alias for them.

### Implementation deliverables and gates

Change: `message.go`, `records.go`, `ports.go`;
`adapters/sqlite/sqlite.go` and its stores; `engine/engine.go`,
`engine/steps.go`, `engine/context.go`; `fleet/filter.go`,
`fleet/fleet.go`, `fleet/outbox.go`; WhatsApp account, channel, and
ingest mapping. Adapt API/CLI/webhook consumers as needed.
[domain-cleanup.md](domain-cleanup.md) separates this identity cleanup
from later setup and payload work.
Decision owner: ADR 0051, with ADR 0006, 0018, 0020, 0021, 0026, 0027,
0029, 0037, 0040, and 0043.

- [x] Replace mandatory phone addressing with an identity key and a
  separate conversation target across inbound, outbound,
  external-reply observations, records, and store calls. Do not rename
  a phone column and use it as an undocumented container for unrelated
  provider IDs.
- [x] Carry provider, subject, and conversation kind/thread/topic
  explicitly. A provider whose ids are local to one store or profile
  composes that stable space into its own subject and thread, so the
  core needs no second identity field. This closes key propagation
  only, not account metadata, binding generation, or delivery
  authorization.
- [x] Remove JID and phone fields, parsing, and parameters from common
  messaging identities, inbound envelopes, store ports, and shared
  identity columns ([domain-cleanup.md](domain-cleanup.md)). Persist a
  neutral account subject and keep the device/session locator in
  adapter-owned storage. Do not hide a device locator under a generic
  identity name, add telephone metadata, or use an unchecked attributes
  map.
- [x] Key registered identities by scoped provider identity and chats
  by target, not sender alone. Update identity/chat/directory SQL,
  indexes, constraints, and store signatures together. Keep sender
  identity distinct from thread identity; a group is not one person's
  identity.
- [x] Replace the obsolete phone-only/identity-only uniqueness
  constraints and every dependent lookup with the neutral ones. The
  schema is edited in place under the pre-release reset policy
  (`../../PLAN.md`), so the WhatsApp backfill and the pre-change
  database gate are gone; the runtime gates below still hold from a
  fresh creation.
- [x] Resolve identities by subject and execution state, holds, and
  guards by chat. Make replies default to the inbound conversation, not
  the sender or the last conversation cached for that sender.
- [x] Remove universal target-to-identity and identity-to-target
  derivation. Carry known participant identity explicitly; a valid
  target must not fabricate its unknown participant
  ([domain-cleanup.md](domain-cleanup.md)).
- [x] Propagate only neutral common template values, including the
  internal identity ID. Remove telephone seeding and validation;
  preserve missing-path errors. The retired WhatsApp references are not
  translated: the `contact` root fails for every bot (owner decision
  2026-10-12), and no phase reintroduces it as an alias.
- [x] Persist the exact conversation target on outbound records and
  queued deliveries. Retain the triggering-chat history convention for
  alternate `to` while carrying the actual destination. This does not
  close binding snapshots or destination-provenance deduplication.
- [x] Persist and test actual delivery provenance independently of the
  triggering history chat. Alternate-recipient provider IDs must
  deduplicate in their destination, not the source chat. Binding
  metadata/generation is implemented in P3 and enforced on queued
  delivery in P5; do not claim that those later requirements are
  already implemented here.
- [x] Finish WhatsApp live events, outbound send, history,
  address-book import, and echo mapping to neutral values. Its
  direct-chat PN behavior remains compatible; shared conversations are
  explicitly filtered/refused until their runtime policy is settled.
  Include the neutral account subject/device lookup and removal of
  common phone metadata; partial mappings do not close it.

Gate:

- [x] Prove opaque own/remote identity subjects and threads need no JID
  or phone field. Recheck provider/store/scope isolation and
  independent sessions, holds, and guards for one sender in two
  supported conversations, including direct conversations whose thread
  differs from the sender.
- [x] Close the database before reopening it, create a fresh
  channel/fleet with no sender cache, and send to the persisted
  conversation. Cover alternate-recipient IDs overlapping source-chat
  IDs. P3/P5 separately prove committed binding identity/generation
  and replacement safety.

## P2 — Extensible definitions, configuration, and selective assembly

Change: `spec/bot.go`, `spec/action.go`; config loader/validation/
schema/writer/reload and template validation; `cmd/botflota/main.go`,
`fleet_cmd.go`, `config_cmd.go`, and reload assembly; fleet
construction; the WhatsApp definition and its options. For
the card slice: `message_content.go`, its codecs, `engine/steps.go`,
and WhatsApp card mappings.
Decision owner: ADR 0052 and 0054, with ADR 0007, 0009, 0018, 0034,
and 0036.

Landed 2026-10-12 on the accepted P1 baseline: `channels` holds the immutable
catalog and the definition type (provider, adapter kind, modes, capabilities,
option schema and strict decode, content and target validation, binding
compatibility, message space, readiness and setup, and the optional builder);
`spec.Bot` names its channel explicitly and `BotSettings` keeps only the shared
pacing/filter fields.
The WhatsApp adapter declares its options, schema, capabilities, and builder;
config validation, schema generation, startup, the watcher, and publication all
consume the same pure catalog. Items 1-4 and
7-10 landed across the P2 commits; the card and readiness halves followed in the
same pass. Every P2 item and gate is checked as of 2026-10-12, with the tests in
`channels`, `config`, `engine`, `fleet`, and `cmd` covering the selector, the
schema/validator agreement, the mixed fleet, shared-resource views, and the
per-bot capability and content-limit checks.

- [x] Introduce the immutable injected catalog of installed
  definitions. Distinguish provider, adapter kind, and account mode;
  select one unambiguous installed implementation. New definitions must
  not require adding provider enums or conditionals throughout shared
  packages.
- [x] Add the accepted channel selector to bot declarations as a required
  field. Reject a missing, unknown, or unavailable selection; there is no
  default provider and no silent fallback.
- [x] Make adapter-owned option types and their strict
  validation/schema declarations extensible through registration. Do not
  add a field or a provider-specific union arm to bot declarations for
  every adapter; do not replace typed runtime values with unchecked
  option maps.
- [x] Separate common pacing/filter settings from adapter-specific
  capabilities and defaults. Adapter settings live only in `channel.options`;
  the old `settings:` keys are removed and fail validation instead of being
  translated. WhatsApp's presence/history defaults apply only to its own
  options and never to another channel. P1 retired `ignore_groups` (a shared
  conversation is refused outright) and no phase resurrects it as a no-op.
- [x] Keep template declarations and card decoding free of per-provider
  aliases ([domain-cleanup.md](domain-cleanup.md)). Common `spec` types and the
  generated schema carry no universal phone field; there is no legacy template
  translation, so the retired `contact.*` root fails as an unknown root for
  every bot. The peer-card shape and its per-adapter variants are the next
  item.
- [x] Implement the agreed neutral peer-card declaration/content shape,
  stored codec, and existing WhatsApp send/ingest mapping in the same
  slice. Do not change declarations in P2 while leaving engine/content
  consumers broken until P5; preserve supported cards and declare
  limits. The neutral shape, codec, and WhatsApp vCard mapping already
  existed; 2026-10-12 added the declared per-adapter variants and their
  per-bot validation, and aligned ADR 0036. The retired telephone-shaped
  declaration keeps no compatibility decoder.
- [x] Inject the same pure declarations into CLI validation, startup,
  watcher loads, schema generation, and publication. Preserve origins,
  secret policy, round-tripping, logical versions, and atomic writes.
  Validation must not open SDK stores, start transports, or perform
  login.
- [x] Validate each consuming bot's graph, entry/exit actions, tools,
  and guard `on_blocked` steps against its installed adapter. Declare
  implemented content and features, and enforce provider-correct limits
  through the definition's own pure `ValidateContent` hook rather than a
  platform-wide promise; default to rejection instead of silent
  downgrade.
- [x] Replace WhatsApp-only fleet construction with registered builders.
  Move adapter option decoding and channel-specific dependency assembly
  into the adapter's integration boundary; initialize device stores or
  auxiliary resources only when the selected adapter/bind method needs
  them. Permit adapter-owned shared dependencies behind independent
  per-binding channel views; config-only operations still perform no
  channel I/O. Landed over 2026-10-12: the catalog builder, the adapter's
  `Prepare` join hook, and per-config assembly that opens the WhatsApp
  device store only when a bot selects it, with explicit no-remote-setup
  and no-local-account surfaces otherwise. The 2026-10-17 remediation moved
  that assembly into one neutral registration per adapter
  (`channels.Installation`, listed in `adapters/installed`): the registration
  opens, validates, and closes the adapter's own resources, and `cmd` iterates
  the installed set without naming an adapter.
- [x] Compare selectors, account modes, options, and binding-relevant
  references on reload. Preserve restart-required transport changes,
  saved/active differences, and isolation of peers. Config publication
  must not replace runtime credentials or silently migrate account
  identity.

Gate:

- [x] A bot without a selector, an unavailable selection, and an unknown
  option field each fail with context; no superseded settings key loads. The
  generated editor schema and runtime validator agree on installed option
  shapes.
- [x] A preconfigured text-only fake joins a mixed fleet through
  registration and runs without WhatsApp devices, QR setup, or
  adapter-specific settings. Common construction has no provider
  switch, and config-only operations perform no channel I/O.
- [x] A registered fake builds two logical channels over one shared
  resource without new common pool or process-management contracts.
  Starting/stopping one view leaves the other usable.
- [x] Unsupported referenced actions/settings fail for the consuming
  bot; invalid publication preserves files and active channels, and a
  restart-required edit does not reconnect a peer scope.
- [x] One shared action/graph is checked independently for a capable
  fake and a text-only fake; the incompatible bot fails with
  file/entity/action/step context even behind a guard or tool path.
  Cover a supported kind with an unsupported variant or known limit
  violation, and two definitions for the same provider with different
  account modes. Use existing modeled content, not a new DSL.

## P3 — Provider-neutral binding lifecycle and bind runtime

Change: `records.go`, `pairing.go`, lifecycle ports in `ports.go`;
binding persistence; `adapters/whatsapp/pair.go`, `pairing.go`, and
account ownership; fleet/startup readiness and assembly.
Decision owner: ADR 0053, with ADR 0021, 0022, 0025, 0028, and 0039.

Landed 2026-10-12 on the accepted P2 baseline: `Binding` carries the
committed association (provider, adapter kind, account mode, namespace,
normalized identity, generation, times); `BindingStore.Commit` assigns
generation one, keeps it when the same provider, adapter kind, account mode,
and identity space renew, and advances it when any of them changes. The
neutral session/step/readiness model (`SetupSession`,
`SetupStep`, `BindingStatus`) and the `AccountLifecycle`/`SetupRunner`
ports live in the core; WhatsApp's `PairManager` speaks the neutral
runner over one engine, and `fleet.SetupFor`
resolves endpoints through the bot's definition. The schema is edited in
place under the pre-release reset policy (`../../PLAN.md`): no migration,
backfill, or legacy result shape survives; the legacy pairing port, session
shape, and status row were deleted on 2026-10-17, not translated. Every P3
item and gate is
checked as of 2026-10-12, with the tests in the core, `stores`,
`whatsapp`, `channel`, `fleet`, and `channels` covering the sessions, the
commit races, the generations, the shared-backend isolation, and the
definition-selected assembly.

- [x] Complete P1's neutral account record with committed binding
  metadata, stable identity-space context where needed, and generation,
  separately from adapter credentials. Implement inspection, bind, and
  unbind operations; select by the bot's definition, never inspect every
  bot through the WhatsApp device store. Remove telephone-bearing
  results and phone-specific errors, with no legacy result shape retained
  ([domain-cleanup.md](domain-cleanup.md)).
  Separate common binding persistence from adapter/auxiliary credential
  stores; do not impose a shared SQLite credential schema on every
  implementation.
- [x] Separate credential presence, provider authorization, transport
  health, and import progress. Readiness exposes safe actionable
  conditions; stored credentials or connection startup return do not
  prove authorization/connection. Missing transport reporting means
  unknown.
- [x] Implement the accepted session/step model: scoped bot, session ID,
  revision, times, safe errors, display challenges, typed write-only
  inputs, external approval, and managed-profile selection. Distinguish
  login QR challenges from public invitations.
- [x] Reuse per-bot mutation serialization and completion waits for
  begin/input/cancel/detach. Workers belong to the instance, not HTTP
  request lifetimes; expiry, retained sessions, retries, and shutdown
  are bounded. Active setup sessions remain in memory, not resumable
  secret storage.
- [x] Stage setup until provider confirmation and local binding commit.
  Preserve the actual outcome across cancellation/expiry, report
  activation failures separately, and handle provider success followed
  by local persistence failure with cleanup or an explicit recovery
  condition.
- [x] Adapt WhatsApp QR pairing and its results to the same neutral
  lifecycle/session machinery, preserving its credential store,
  confidentiality, and successful-pairing/history-wait protection.
  Import completion must not define generic account success.
- [x] Support same-identity reauthorization and explicit identity
  replacement through the same contracts. Preserve stable identity
  spaces, verify ownership, serialize with sends, and keep history.
  Detachment forgets the local binding; it does not destroy an account
  or managed profile. Retire the old binding's observations before a
  replacement starts; temporary backend context selection changes
  neither binding identity nor generation.

Gate:

- [x] Executable fakes complete QR, credential-only, multi-step
  approval, and local-profile setup without phone fields or a universal
  QR. Reconnect/restart preserves committed identity spaces.
- [x] Test stale input/session IDs, QR rotation versus session expiry,
  duplicate request keys, cancellation versus commit, detach during
  import, commit then activation failure, and shutdown. No
  stale/cancelled worker activates later, and successful setup never
  activates twice.
- [x] An unconfigured or revoked bot remains inspectable/setup-capable
  without stopping peers; detachment keeps its chats, directory,
  history, and the actual outcome of any completed setup.
- [x] Two fixed bindings share a fake backend: selecting one context
  leaves both identities and generations unchanged; detach/replacement
  cannot reassign stale events or destroy the peer's profile. Credential
  ownership can remain outside common binding storage.

## P4 — Common API and CLI, not provider-specific control planes

Change: `api/api.go`, `binding.go`, `views.go`, `params.go`,
`send.go`, chat control/sync, history operations/export;
`cmd/botflota/bot_cmd.go`, `chat_cmd.go`, `message_cmd.go`,
`directory_cmd.go`, client views, and help; peer-first-seen webhook
serialization.
Decision owner: ADR 0053 and 0054, with ADR 0042, 0044, 0045, and 0046.

Public-contract decisions closed with the owner on 2026-10-12, and the
implementation landed across the P4 commits: neutral lifecycle/bind/
capability wiring, the `/binding/setups` operations, per-bot discovery
with explicit unsupported outcomes, neutral peer/chat/binding references
with adapter-owned send admission, the `peer` and `graph-run` CLI groups,
neutral webhook/export provenance, adapter-owned binding-compatibility
enforcement before history migration, and scoped authorization with
redaction throughout. ADR 0054 owns public
names matching the selected domain vocabulary, ADR 0053 the `/binding/setups`
lifecycle, ADR 0044 neutral references and committed-binding send admission,
ADR 0052 discovery/unsupported-operation views, and ADR 0042 ZIP/CSV sections.
ADR 0045 matches the already selected `peer.first_seen` envelope. See
[open-decisions.md](open-decisions.md) for the closure of P4's public contracts
and P5's delivery, reception, and provenance contracts.
The checkboxes below are checked as that landed implementation.

- [x] Wire neutral lifecycle/bind/capability dependencies into the API.
  Remove constructor requirements for WhatsApp pairing and unsupported
  live features; distinguish reading already stored history and
  directory from invoking a currently available channel operation.
- [x] Implement scoped channel/binding/method discovery and bind
  begin/read/input/cancel operations. Align public routes with
  ADR 0053 before changing endpoints; scope and bot
  remain part of every operation's ownership
  ([open-decisions.md](open-decisions.md)).
- [x] Expose declared support, account authorization, and conversation
  eligibility as separate facts. Unknown never implies permission; a
  disconnected bot keeps support without invented rights. Translate
  absent optional operations to explicit unsupported outcomes, not
  internal errors.
- [x] Use neutral peer/chat/binding references for reads, controls,
  sync, and sends. Resolve chat IDs under scope and bot, never by ID
  alone. WhatsApp selectors and routes are the neutral ones; no legacy
  selector, old route, or phone output is retained, and shared code never
  inspects the shape of a neutral subject ([domain-cleanup.md](domain-cleanup.md)).
- [x] Replace API-wide digit validation with the selected adapter's
  target rules and common admission checks. Preserve atomic API sends,
  `client_id` reconciliation, chat-control semantics, and the default
  reply path without assuming arbitrary-address outreach is permitted.
  Reject new unbound sends before persistence with 409 `binding_required`;
  preserve duplicate reconciliation and do not confuse disconnected with unbound
  (ADR 0044). P5 adds durable generation snapshots and delivery enforcement.
- [x] Make operational CLI commands consume neutral API views/selectors
  and common setup steps. Commands, arguments, fields, and results match the
  selected domain names: `bot bind`/`bot unbind`, peer, directory, chat, and
  graph run, with no retired command aliases. Keep the locked offline operator
  path and adapted WhatsApp workflows working through the neutral operations;
  an adapter with existing step kinds needs no CLI handler or provider form.
- [x] Carry neutral identity/provenance through webhooks and CSV export,
  with no legacy field retained and no compatibility boundary. A serialized
  envelope or archive change is breaking by default; do not put a universal
  phone field back into the event or store contract.
  Enforce provider and identity-space compatibility between the two
  bots' bindings before history migration; never present unreadable
  local targets as valid for a replacement bot.
- [x] Authorize before reading binding/session data; keep out-of-scope
  targets opaque (404). Enforce stale-session/step conflicts,
  request-key retry safety, bounded input bodies and budgets,
  authenticated non-cacheable challenges, and redaction across JSON,
  logs, errors, and exports.

Gate:

- [x] A scoped HTTP client completes each fake setup mode and
  reauthorization without filesystem access, then reads a conversation,
  holds/releases it, and queues a text send through the same API.
- [x] Personal CLI operation uses those operations while the instance
  runs, with no database fallback; existing WhatsApp pairing, query,
  and send commands keep working through the neutral operations.
- [x] Cross-scope reads/inputs/cancellation, stale IDs, unsupported
  methods, and lost-response retries obey the accepted errors. Submitted
  tokens/passwords/codes are never echoed or exported.
- [x] Per-bot discovery and API admission agree with config validation
  on declared capabilities. Unsupported submitted content/operations are
  refused before queueing or invoking the absent operation. A
  disconnected bot retains its declaration without invented dynamic
  permission.

## P5 — Delivery, replay, and optional operations without protocol leakage

Change: transport/error contracts in `ports.go` and `message.go`;
`fleet/outbox.go` and actor lifecycle/control;
turn/send/message/outbox/history stores; WhatsApp
send/events/history/ingest.
Decision owner: ADR 0052, with ADR 0004, 0011, 0035, 0036, 0040,
and 0044.

Its delivery, reception, and provenance contracts closed with the owner on
2026-10-17 (see [open-decisions.md](open-decisions.md)). P5 landed on
2026-10-17: every implementation item and every gate below is checked, with
the tests in `fleet`, `engine`, `adapters/whatsapp/channel`,
`adapters/whatsapp/ingest`, `adapters/sqlite/stores`, `api`, `channels`, and
`config` covering the send outcomes, binding gates, replay completion,
message space, history and observation normalization, and shared-source
backpressure.

- [x] Enforce classified send outcomes: permanent invalid/unsupported
  input fails immediately; transient failures retry within bounds and
  honor retry hints. Losing binding authorization stops that bot with
  its queue pending; replacing the binding fails its previous entries.
  Do not classify outcomes by parsing provider error text in shared code.
- [x] Validate rendered content/targets before queue admission and
  recheck dynamic eligibility at delivery. Never silently split one
  durable message into multiple wire sends or downgrade content. Apply
  an already-supported explicit fallback before dependent side effects;
  this plan does not create a new fallback DSL.
- [x] Check the stored binding generation at admission and immediately
  before sending. Serialize detach/replacement with in-flight delivery;
  old pending entries cannot inherit a new identity. Apply ADR 0044's closed
  no-binding admission decision: reject a new send instead of guessing a
  future binding. P4's API check does not replace these durable enforcement
  gates; see [open-decisions.md](open-decisions.md).
- [x] Make send success mean downstream acceptance, not recipient
  delivery/read confirmation. Preserve the existing status contract
  through documented translation, optional provider IDs, and
  at-least-once crash behavior; report uncertain timeout outcomes
  honestly.
- [x] Make channel lifetime work independently of SDK assumptions: one
  inbound reader, concurrent serialized outbox sending, adapter-owned
  stream closure, bounded buffering, cancellation, partial-start
  cleanup, and bounded disconnect. Do not reuse stopped instances
  accidentally. This is a logical per-binding lifetime. A shared
  physical reader stays adapter-owned; releasing one view must not close
  peer views. Shared transport failure is not authorization revocation.
- [x] Normalize history/backfill and external-reply observations to the
  same conversation/binding values. Keep wire anchors opaque; no phone
  requirement or unconditional optional-feature calls. Preserve inert
  imports, peer-first-seen rules, own-send echo correlation, and held
  inbound.
- [x] Exercise upstream replay/cursor behavior against processing
  success, failure, filtering, and suppression. Inbound handoff is not
  proof of durable commit; settle any required processing
  acknowledgement before advancing irreversible cursors. Do not add a
  universal durable inbox without its decision, or claim replay
  guarantees a channel cannot supply. The completion contract is closed in
  ADR 0004/0040 (see
  [open-decisions.md](open-decisions.md)): distinguish confirmed
  processing, persisted duplicates, deliberate discards, and failures.
  Keep source update IDs and receive cursors out of message identity; do
  not advance confirmation on handoff alone.
- [x] Verify message-ID space across binding replacement and
  retained/migrated history. Preserve destination and any required
  account provenance independently of the triggering history chat.
  The durable key is settled in ADR 0051/0026, so choose SQL constraints
  from it (see [open-decisions.md](open-decisions.md)); binding
  generation alone is not proof of the provider's message-ID namespace.
- [x] Exercise bounded routing from a shared source to a slow or stopped
  logical consumer. Document the coupled backpressure/replay boundary
  rather than promising independent progress or adding unapproved
  durable buffering. Deployment policy remains TBD in
  [open-decisions.md](open-decisions.md). The preparation's backpressure
  boundary itself is closed in ADR 0052; only deployment policy stays TBD.
- [x] Preserve typed content and stored codecs. Keep selections, message
  IDs, callback/event IDs, and control traffic conceptually distinct;
  unsupported interactions must not masquerade as text turns. Test the
  extension boundary without shipping unused button/editing/receipt
  machinery.
- [x] Apply admission/delivery capability checks to P2's neutral
  peer-card payload. No common telephone parser or provider-specific
  payload map; preserve supported WhatsApp cards and reject unsupported
  card variants before queue admission.

Gate:

- [x] Permanent failure does not block FIFO through useless retries;
  transient sends retry, account authorization loss is visible, and a
  replacement account sends none of the old queue.
- [x] Test restart around send/recording, reused message IDs in separate
  destinations, own-send echoes versus human replies, held inbound
  recording, and channels without optional operations.
- [x] Slow/cancelled producers and partially started adapters exit
  without leaks or dropped messages from full queues. Tests establish
  the actual acknowledgement/replay boundary, not an inferred
  exactly-once guarantee or recipient-delivery claim.
- [x] A replay-capable fake confirms only after the agreed processing
  outcome; failed persistence remains replayable, and recorded
  duplicates and deliberate discards follow their explicit rules.
  Shared-source tests cover view teardown, context-selection races, and
  bounded backpressure.
- [x] A statically valid template whose resolved payload violates the
  consuming bot's declared limits fails before queue admission, as does
  equivalent API input. Delivery rechecks a dynamic eligibility change
  separately from declared support; unsupported queued content fails
  without silent downgrade or useless retries. No test assumes earlier
  external effects can be rolled back.

## P6 — Prove extension locality and close regression coverage

Change: cross-package contract/regression tests and fixtures, plus the
remaining documentation and generated reference that must describe the
implemented runtime rather than the accepted target.

P6 implementation landed on 2026-10-17: the reusable conformance adapter and
harness (`adapters/conformance`, `adapters/testchannel`), the single-fixture
contrast across the WhatsApp mapping and the registered fakes, the
shared-backend and update-source coverage, the domain/hot-path audit, and
the documentation, schema, and `PLAN.md` alignment. Every automated gate
passes. The WhatsApp operational manual checklist in
[`../guide/whatsapp.md`](../guide/whatsapp.md) (pairing, reconnect, live
messages, queued replies, history import/backfill, unpair) is pending
operator exercise, so P6 stays open until it is exercised.

- [x] Run the same fixture graph through WhatsApp's test mapping and
  contrasting registered fakes: an opaque-ID text-only channel and a
  profile-local channel. Both traverse actual engine, stores,
  fleet/outbox, and API paths, not bespoke test-only execution
  pipelines. Include two bindings sharing one fake backend and an
  HTTP-style update source with processing confirmation. These are
  behavioral contrast cases, not new provider branches or SDK
  dependencies.
- [x] Add an independent test adapter using only its adapter files and
  one registry entry. Prove setup with existing steps, strict option
  validation/schema, discovery, conversation reads, filters/holds,
  durable sends, restart, and detach work without editing shared
  production code. A required port/config/API/store change fails the
  extension-locality gate; fix the abstraction instead of adding another
  exception. Publish this reusable conformance test path for adapters.
  Include the capability-consistency gates from plan.md §2 and P2/P4/P5
  so future content/operation extensions cannot bypass config checks,
  runtime/API admission, or per-bot discovery.
- [x] Review shared dependencies and hot paths: no SDK/wire identities,
  phone-only routing, QR-only readiness, unconditional WhatsApp stores,
  or provider switches outside registration
  boundaries. Apply
  [domain-cleanup.md](domain-cleanup.md)'s stricter
  field/parameter/context audit: optional phone fields also fail the
  clean-domain gate. Keep transport, setup, support, and storage
  separate.
- [x] Complete automated fresh-schema/reopen, scope-isolation,
  publication/reload, setup-race, retry, cancellation, and redaction
  coverage from the earlier gates. Fakes declare unsupported features
  honestly and require no new SDK, network service, or production
  credential.
- [x] Update `glossary.md`, `architecture.md`, `api.md`,
  `integration.md`, relevant guides/help, and config examples to match
  the code. Regenerate the schema from implemented Go types. Do not
  repeat the completed ADR-writing phase; amend an ADR only for a real
  unresolved decision.
- [x] Align `PLAN.md` Phase 7's pre-P1 deliverable descriptions —
  `phone`, `contact`, `pair`/`unpair`, `contact.first_seen` — with the
  neutral contracts, or mark them superseded, so the plan describes the
  implemented code instead of the retired shapes.
- [ ] Run `make check` and `make test-race` in the main repository with
  required permission, and complete the WhatsApp manual checklist.
  Report manual checks as pending until actually exercised. Both
  automated checks passed on 2026-10-17; the WhatsApp manual checklist
  is PENDING operator exercise and was not run here, so this item stays
  open.

Gate:

- [ ] The extension-locality demonstration and every automated gate
  pass; WhatsApp pairing, reconnect, live messages, queued replies,
  history import/backfill, and unpair remain operational. Its automated
  half passes and was verified here; the WhatsApp operational manual
  checklist remains PENDING operator exercise (real credentials and a
  device are needed) and was not run, which is why this bullet stays
  open.
- [x] P1–P6 code outcomes are complete and tracked in `../../PLAN.md`.
  Adding an adapter for existing capabilities now means adapter work
  plus registration, not another runtime architecture project.
