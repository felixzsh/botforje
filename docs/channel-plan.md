# Messaging channel preparation plan

Status: architectural decisions accepted; runtime preparation not implemented.
Code baseline: `3544357`, inspected on 2026-10-08. ADR 0051–0054 own the accepted decisions.
The completed documentation-only P0 is not part of the execution checklist below. Described
reference cases are not executable fixtures, and an accepted ADR is not a passing runtime gate.
`../PLAN.md` Phase 7 owns overall progress; this document defines the P1–P6 code outcomes and gates.
`../ROADMAP.md` owns the later implementation of actual additional channels.

## 1. Outcome: adding a channel is adapter work

Prepare Botflota to operate through messaging channels without assuming any one provider.
WhatsApp becomes an ordinary registered adapter, not the runtime's implicit account model.
Later Signal, SimpleX Chat, or Telegram work should concentrate on the adapter, its protocol,
credentials, options, and capability declaration, rather than reopening the messaging architecture.

- Expected changes: its implementation and tests under `adapters/`, one composition-root
  registration, approved dependencies, and adapter-specific examples or guides. Generated schema
  changes are allowed when they follow the registered option types.
- Unexpected changes: transport ports, shared domain records, `spec.Bot`, generic config logic,
  engine routing, fleet control, generic API/CLI handlers, or shared persistence schema/stores.
  Needing those changes to onboard an already-modeled channel means this preparation is incomplete.
- A genuinely new capability may justify a separate core extension. It must not be disguised as
  raw protocol JSON, arbitrary commands, or a growing provider switch in shared code.

The final gate must demonstrate this rule by registering an independent test adapter after the
shared runtime is in place. Merely making a struct satisfy `MessageChannel` is insufficient.

## 2. Architectural boundaries

| Owner | Responsibility after preparation |
| --- | --- |
| Core | Neutral messages, addressing, account metadata, errors, and small consumer-owned ports. |
| `spec` | Channel selection and common declarative behavior; no SDK or per-provider bot structs. |
| `config` | Strict decoding, pure support checks, publication, schema, and reload comparison. |
| `engine` | Execute the graph against neutral contacts and conversations, never protocol rules. |
| `fleet` | Actor lifetime, chat control, pacing, and durable delivery through injected ports. |
| `api` and CLI | Authorized operations over the same neutral account and conversation contracts. |
| Adapter | Protocol, identity mapping, credentials, options, setup, and dynamic eligibility. |
| Composition root | Register definitions and assemble only the selected adapters' dependencies. |

Keep `MessageChannel` centered on `Connect`, `Disconnect`, `Send`, and `Incoming`; preparation
may refine their shared values and errors without creating a universal interface for every feature.
Account lifecycle and interactive setup belong beside transport, not inside `Connect`.
Reuse optional ports such as `StateReporter`, `OutboundObserver`, and `HistorySyncer` where they
fit.
Do not add unused receipt/editing ports, runtime plugins, reflection-based registration, or `init`.
The core remains standard-library-only; `config` never imports adapters or `engine`.

Both operating paths must work: an operator using YAML and the CLI, and a host backend using the
scoped API for remote account setup and messaging. The host owns its callers and interface;
Botflota owns setup, credential custody, runtime, and scope authorization (ADR 0046).
Remote callers need no SSH, runtime file edits, or database access. Provider approval may still
require their messaging app. No host credential or reusable channel secret reaches the browser.

This plan implements architectural preparation, not new production adapters, provider SDKs,
a dashboard, cross-provider contact merging, shared-account dispatch, or infrastructure
provisioning.
No dependency is approved here. Group graph policy and new interactive DSL features remain separate
work; unsupported shared conversations must be refused, not treated as direct messages (ADR 0051).

## 3. Execution rules

Deliver P1–P6 in order, preserving WhatsApp operation at every phase boundary. Each phase includes
the affected consumers, executable tests, and compatibility translations needed to keep the tree
working; do not leave public ports changed while their callers remain on the previous model.

- Check a task only when its code and tests exist; check a phase only when its gate passes.
- Update the corresponding `../PLAN.md` deliverable in the commit that completes the phase.
- Transitional WhatsApp translations belong at named compatibility boundaries, not throughout the
  engine, fleet, or stores. They must not become a second independent account/setup implementation.
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
`fleet/outbox.go`; WhatsApp channel and ingest mapping. Adapt existing API/CLI consumers as needed.
Decision owner: ADR 0051, with ADR 0020, 0026, 0027, 0029, 0037, 0040, and 0043.

- [ ] Replace mandatory phone addressing with a contact key and a separate conversation target
  across inbound, outbound, external-reply observations, records, and store calls. Do not rename a
  phone column and use it as an undocumented container for unrelated provider IDs.
- [ ] Carry provider, stable namespace, subject, conversation kind/thread/topic, and account
  binding explicitly. Persist safe account metadata and generation separately from adapter secrets;
  move JID parsing and PN/LID resolution out of the core and into WhatsApp handling.
- [ ] Key contacts by scoped provider identity and chats by their conversation, not by sender alone.
  Update contact/chat/address-book SQL, indexes, constraints, and store signatures together.
  Keep sender identity distinct from thread identity; a group is not a person's contact.
- [ ] Implement the approved data-preserving WhatsApp upgrade and backfill. Preserve numeric IDs,
  foreign keys, sessions, guards, deduplication, queued targets, and device credentials. Replace
  obsolete phone-only/contact-only uniqueness constraints; adding columns alone is insufficient.
  Do not require an existing database to be discarded or recreated.
- [ ] Resolve contacts by subject and sessions, holds, and guards by chat. Make replies default to
  the inbound conversation, not the sender or the last conversation cached for that sender.
- [ ] Propagate neutral runtime context values and validate their template paths. Keep real phone
  data as optional metadata and `contact.phone` as its defined empty value when unavailable;
  preserve missing-path errors and expose the neutral contact ID (ADR 0018).
- [ ] Persist the exact target and binding on `SendRecord`, `OutboundRecord`, and `OutboxEntry`.
  Retain the triggering-chat history convention for alternate `to` while carrying the actual
  destination's provenance and dedup namespace. Never select another account/provider implicitly.
- [ ] Adapt WhatsApp live events, outbound send, history, address-book import, and echo mapping to
  the neutral values. Its direct-chat PN behavior remains compatible; shared conversations are
  explicitly filtered/refused until their runtime policy is settled.

Gate:

- [ ] Upgrade a pre-change database, reopen it, and verify IDs, sessions, history, guards, dedup,
  outbox targets, and WhatsApp credentials survive. A repeated upgrade is safe.
- [ ] Prove identical external IDs in different providers/profiles/scopes cannot collide, and one
  sender in two supported fake conversations has independent sessions, holds, and guards.
- [ ] Restart a durable outbox and send to its persisted conversation/binding, with no sender cache;
  cover alternate-recipient IDs that overlap those in the triggering chat.

### P2 — Extensible definitions, configuration, and selective assembly

Change: `spec/bot.go`, `spec/action.go`; config loader/validation/schema/writer/reload and template
validation; `cmd/botflota/main.go`, `fleet_cmd.go`, `config_cmd.go`, and reload assembly;
`fleet.BotSpec`/construction; the WhatsApp definition and its option translation.
Decision owner: ADR 0052 and 0054, with ADR 0007, 0009, and 0034.

- [ ] Introduce the immutable injected catalog of installed definitions. Distinguish provider,
  adapter kind, and account mode; select one unambiguous installed implementation. New definitions
  must not require adding provider enums or conditionals throughout shared packages.
- [ ] Add the accepted channel selector to `spec.Bot`, preserving missing-selector WhatsApp native
  compatibility. Reject explicit unknown/unavailable selections, never silently fall back.
- [ ] Make adapter-owned option types and their strict validation/schema declarations extensible
  through registration. Do not add a field or a provider-specific union arm to `spec.Bot` for
  every adapter; do not replace typed runtime values with unchecked option maps.
- [ ] Separate common pacing/filter settings from adapter-specific capabilities and defaults.
  Preserve old WhatsApp YAML through its compatibility translation, without enabling WhatsApp's
  presence/history defaults on other channels or ignoring explicitly unsupported settings.
- [ ] Inject the same pure declarations into CLI validation, startup, watcher loads, schema
  generation, and publication. Preserve origins, secret policy, round-tripping, logical versions,
  and atomic writes. Validation must not open SDK stores, start transports, or perform login.
- [ ] Validate each consuming bot's graph, entry/exit actions, tools, and guard `on_blocked` steps
  against its installed adapter. Declare implemented content/features and provider-correct limits,
  not a platform-wide promise; default to rejection instead of silent downgrade.
- [ ] Replace WhatsApp-only fleet construction with registered builders. Move `whatsappSettings`
  and channel-specific dependency assembly into the adapter's integration boundary; initialize
  device stores or auxiliary resources only when the selected adapter/setup needs them.
- [ ] Compare selectors, account modes, options, and binding-relevant references on reload.
  Preserve restart-required transport changes, saved/active differences, and isolation of peers.
  Config publication must not replace runtime credentials or silently migrate account identity.

Gate:

- [ ] Old YAML works; explicit unavailable selections and unknown option fields fail with context.
  The generated editor schema and runtime validator agree on installed option shapes.
- [ ] A preconfigured text-only fake joins a mixed fleet through registration and runs without
  WhatsApp devices, QR setup, or adapter-specific settings. Common construction has no provider
  switch, and config-only operations perform no channel I/O.
- [ ] Unsupported referenced actions/settings fail for the consuming bot; invalid publication
  preserves files and active channels, and a restart-required edit does not reconnect a peer scope.

### P3 — Provider-neutral account lifecycle and setup runtime

Change: `records.go`, `pairing.go`, account/setup ports in `ports.go`; linked-account persistence;
`adapters/whatsapp/pair.go`, `pairing.go`, and account ownership; fleet/startup readiness and
assembly.
Decision owner: ADR 0053, with ADR 0021, 0022, 0025, 0028, and 0039.

- [ ] Replace mandatory JID/pairing assumptions with neutral binding metadata, `AccountLifecycle`,
  and the setup runner. Select the account implementation by the bot's definition, never inspect
  every bot through the WhatsApp device store. Keep protocol code in the adapter.
- [ ] Separate credential presence, provider authorization, transport health, and import progress.
  Readiness exposes safe actionable conditions; stored credentials or a successful `Connect`
  return do not prove authorization/connection. Missing transport reporting means unknown.
- [ ] Implement the accepted session/step model: scoped bot, session ID, revision, times, safe
  errors, display challenges, typed write-only inputs, external approval, and managed-profile
  selection. Distinguish login QR challenges from public contact invitations.
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
  contracts. Preserve stable namespaces, verify ownership, serialize with sends, and retain history.
  Detachment forgets the local binding; it does not destroy a provider account or managed profile.

Gate:

- [ ] Executable fakes complete QR, credential-only, multi-step approval, and local-profile setup
  without fabricated phones or a universal QR. Reconnect/restart preserves committed namespaces.
- [ ] Test stale input/session IDs, QR rotation versus session expiry, duplicate request keys,
  cancellation versus commit, detach during import, commit then activation failure, and shutdown.
  No stale/cancelled worker activates later, and successful setup never activates twice.
- [ ] An unconfigured or revoked bot remains inspectable/setup-capable without stopping peers;
  detachment keeps its chats, contacts, history, and the actual outcome of any completed setup.

### P4 — Common API and CLI, not provider-specific control planes

Change: `api/api.go`, account/pairing handlers, `views.go`, `params.go`, `send.go`, chat
control/sync,
history operations/export; `cmd/botflota/bot_cmd.go`, `chat_cmd.go`, `message_cmd.go`,
`address_book_cmd.go`, client views, and help; first-contact webhook serialization.
Decision owner: ADR 0053 and 0054, with ADR 0042, 0044, 0045, and 0046.

- [ ] Wire neutral lifecycle/setup/capability dependencies into the API. Remove constructor
  requirements for WhatsApp pairing and unsupported live features; distinguish reading already
  stored history/address books from invoking a currently available channel operation.
- [ ] Implement scoped channel/account/method discovery and enrollment begin/read/input/cancel
  under the accepted `B/account` routes (ADR 0053), where
  `B = /api/v1/scopes/{scope}/bots/{bot}`; add `GET B/channel` (ADR 0052).
- [ ] Expose declared support, account authorization, and conversation eligibility as separate
  facts. Unknown never implies permission; a disconnected bot keeps support without invented rights.
  Translate absent optional operations to explicit unsupported outcomes, not internal errors.
- [ ] Use neutral contact/chat/account references for reads, controls, sync, and sends. Resolve
  chat IDs under scope and bot, never by ID alone. Retain WhatsApp phone selectors and `/pairing`
  as explicit compatibility translations over the same operations, not alternate implementations.
- [ ] Replace API-wide digit validation with the selected adapter's target rules and common
  admission checks. Preserve atomic API sends, `client_id` reconciliation, chat-control semantics,
  and the default reply path without assuming arbitrary-address outreach is permitted.
- [ ] Make operational CLI commands consume neutral API views/selectors and common setup steps.
  Keep the locked offline operator path and existing WhatsApp commands compatible; adding a new
  adapter with existing step kinds must not require another CLI handler or provider form.
- [ ] Carry neutral identity/provenance through webhooks and CSV export, preserving existing
  compatible fields. Enforce provider/namespace compatibility before history migration; do not
  present unreadable local targets as valid conversations for a replacement bot.
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
- [ ] Normalize history/backfill and external-reply observations to the same conversation/binding
  values. Keep wire anchors opaque; no phone requirement or unconditional optional-feature calls.
  Preserve inert imports, first-contact rules, own-send echo correlation, and held inbound history.
- [ ] Exercise upstream replay/cursor behavior against processing success, failure, filtering,
  and suppression. `Incoming` handoff is not proof of durable commit; settle any required
  processing acknowledgement before advancing irreversible cursors. Do not add a universal
  durable inbox without its decision, or claim replay guarantees a channel cannot supply.
- [ ] Preserve typed content and stored codecs. Keep selections, message IDs, callback/event IDs,
  and control traffic conceptually distinct; unsupported interactions must not masquerade as text
  turns. Test the extension boundary without shipping unused button/editing/receipt machinery.

Gate:

- [ ] Permanent failure does not block FIFO through useless retries; transient sends retry,
  account authorization loss is visible, and a replacement account sends none of the old queue.
- [ ] Test restart around send/recording, reused message IDs in separate destinations, own-send
  echoes versus human replies, held inbound recording, and channels without optional operations.
- [ ] Slow/cancelled producers and partially started adapters exit without leaks or dropped
  messages from full queues. Tests establish the actual acknowledgement/replay boundary, not an
  inferred exactly-once guarantee or recipient-delivery claim.

### P6 — Prove extension locality and close regression coverage

Change: cross-package contract/regression tests and fixtures, plus the remaining documentation
and generated reference that must describe the implemented runtime rather than the accepted target.

- [ ] Run the same fixture graph through WhatsApp's test mapping and contrasting registered fakes:
  an opaque-ID text-only channel and a namespace-local profile channel. Both traverse actual
  engine, stores, fleet/outbox, and API paths, not bespoke test-only execution pipelines.
- [ ] Add an independent test adapter using only its adapter files and one registry entry. Prove
  setup with existing steps, strict option validation/schema, discovery, conversation reads,
  filters/holds, durable sends, restart, and detach work without editing shared production code.
  A required port/config/API/store change fails the extension-locality gate; fix the abstraction
  instead of adding another exception. Publish this reusable conformance test path for adapters.
- [ ] Review shared dependencies and hot paths: no SDK/wire identities, phone-only routing, QR-only
  readiness, unconditional WhatsApp stores, or provider switches outside named compatibility and
  registration boundaries. Keep transport, setup, optional support, and storage separate.
- [ ] Complete automated data-upgrade, scope-isolation, publication/reload, setup-race, retry,
  cancellation, and redaction coverage from the earlier gates. Fakes declare unsupported features
  honestly and require no new SDK, network service, or production credential.
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

## 5. Remaining decisions: close only the affected boundary

The completed ADR work is not a new implementation phase. These operational gaps must still be
resolved with the user and recorded in their owners before incompatible behavior is coded:

- **P1, addressing/dedup:** settle exact account-versus-contact namespaces, alternate-destination
  dedup provenance, and legacy queue backfill when stored account information is missing. ADR 0051
  and 0026 must agree; preservation cannot mean fabricating an account or retaining collisions.
- **P2, selection:** ADR 0054's provider/mode selector must resolve the adapter kind unambiguously.
  Decide how indistinguishable installed implementations are rejected or selected before supporting
  them; do not invent precedence or turn adapter kind into an account mode.
- **P5, unbound sends:** ADR 0044 currently accepts sends to an unpaired bot, but the new queue must
  be tied to the account authorized at acceptance. Decide whether no-binding admission is rejected
  or has another explicit safe contract; it cannot automatically attach to a later identity.
- **P5, receive commit boundary:** if a replay/cursor channel needs a durable-processing signal,
  settle the narrow acknowledgement contract in ADR 0004/0040 before claiming that support.
  Do not silently promote a channel handoff into a durable inbox or a committed turn.

Credential encryption/key custody before storing a new real credential type (ADR 0053), and trusted
auxiliary endpoint/profile deployment (ADR 0054), remain gates for the corresponding future adapter.
They do not require implementing new providers to complete this preparation. Fakes use synthetic
inputs; a scoped caller must never choose arbitrary server paths, databases, or unmanaged endpoints.
Do not assume ADR 0047–0049's file-root, egress, or secret-catalog protections are implemented.

## 6. Reference cases, not adapter implementation instructions

Use these differences to challenge the contracts and fakes, not to select SDKs or promise support:

- WhatsApp: PN/LID resolution and device JIDs stay inside its adapter; QR setup, presence, history,
  and address-book import remain implemented features, not obligations for other channels.
- Telegram Bot API: write-only bot-token setup, separate user/chat IDs, conversation-local message
  IDs, and dynamic eligibility [T1]. A token is not login to a person's account or permission for
  arbitrary outreach. Connected accounts and multi-step user login are separate modes [T2–T5];
  shared-service dispatch is not implied by registering an adapter.
- SimpleX Chat: profile-local identities and conversation targets, managed auxiliary transport,
  contact invitations distinct from login QR, and asynchronous send acceptance [S1–S4]. Do not
  infer an independent server login to an existing mobile profile from desktop linking [S5].
- Signal: a future extension target, not a verified setup/protocol contract in this plan. Its real
  adapter needs its own supported-flow investigation; never derive its identity from a presumed
  phone or universal QR just because the existing adapter uses them.

The required baseline is authorized direct text messaging plus usable account setup/binding.
Media, polls, formatting, buttons, groups/topics, receipts, history, address books, and external
reply observation are separately declared optional capabilities. A new feature is implemented with
its first real consumer; a provider's platform support does not automatically become adapter
support.

## References

Provider references were inspected on 2026-10-07; recheck them when a real adapter starts.
The SimpleX `stable` documentation is mutable, not a pinned SDK guarantee.

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

[simplex-commands]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/COMMANDS.md
[simplex-types]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/TYPES.md
[simplex-events]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/EVENTS.md
