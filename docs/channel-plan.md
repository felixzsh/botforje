# Messaging channel preparation plan

Status: accepted; the P0 decision gate closed on 2026-10. The decisions live in ADR 0051–0054 and
P1–P6 are tracked in `../PLAN.md` Phase 7; the names below are frozen by those ADRs.
Baseline: repository commit `90eab2194676795167ba31f26d9013b2c65717d7`, inspected on 2026-10-07.

Prepare the existing runtime for different messaging providers and account modes without
implementing Telegram or SimpleX Chat yet. Every new type, method, route, and setting below is the
accepted target, not available today.
`../PLAN.md` owns current work; `../ROADMAP.md` owns additional channels.

## 1. Outcome and boundaries

Two operation paths must use the same account and messaging contracts:

- Personal operation: an operator configures a bot and uses the CLI, with no host application.
- Remote operation: a person uses a host's web interface to configure a messaging identity for
  customer conversations. The host backend calls Botflota's scoped API; the browser never receives
  its owner token, scoped token, or reusable channel session material.

Botflota owns channel account setup, credential custody, execution, and scope authorization.
The host owns its callers, interface, resource mapping, and application permissions
(`integration.md`; ADR 0046). Provider approval may still require the person's messaging app.
Remote operation means no SSH, manual runtime file edits, or direct database access by that person;
it does not mean Botflota can bypass provider approval or provision infrastructure itself.

The preparation must preserve the WhatsApp path and prove another account model with fakes.
It must not build provider SDKs, a dashboard, browser sign-in, cross-provider contact merging,
shared-account dispatch, arbitrary protocol commands, or infrastructure provisioning.
No new dependency is approved by this plan.

## 2. What the code actually does today

- `ports.go`: `MessageChannel` has `Connect`, `Disconnect`, `Send`, and `Incoming`. `Send` returns
  one provider message ID, possibly empty. `StateReporter`, `OutboundObserver`, and `HistorySyncer`
  are already optional; keep this separation rather than building a universal large interface.
- `message.go`, `records.go`, and `ports.go`: inbound/outbound addresses are `FromPhone` and
  `RecipientPhone`; contacts and store calls use phones; `LinkedAccount` holds a JID and parses it
  in the core. `pairing.go` assumes QR, a phone result, and WhatsApp history statistics.
- `spec/bot.go`: no channel selector exists. Presence, pacing, filters, history, and address-book
  settings share `BotSettings`. `spec/action.go` describes `to` as a phone or template.
- `cmd/botflota/fleet_cmd.go`: startup opens WhatsApp devices, checks WhatsApp pairings for every
  bot, builds only the WhatsApp channel, translates its settings, and injects its pair manager.
- `api/api.go`: linked-account, pairing, unpairing, and address-book dependencies are required
  even when a channel would not support those operations. The API itself imports no whatsmeow.
- `api/pairing.go`: authenticated, scope-addressed POST/GET/DELETE pairing already expose the QR
  in `code`. There is no session ID or operation to submit an OTP, token, or password.
  `adapters/whatsapp/pairing.go` runs these sessions in memory and fixes the method to `qr`.
- `api/views.go`: bot views infer account readiness from local credentials and pairing state;
  contact views and `api/params.go` resolve chats by phone. `api/send.go` accepts only text and
  validates `to` as plain digits. Account deletion is already remote in `api/account.go`.
- `engine/engine.go`: contact lookup, deduplication, sessions, and operator observations resolve
  a contact's phone. `engine/steps.go` replies to that phone, not an explicit conversation.
  Alternate-recipient replies remain stored under the triggering chat.
- `fleet/filter.go` filters phones. Chat holds in `fleet/fleet.go` are keyed by phone.
  `adapters/whatsapp/channel/events.go` caches the last conversation per sender; this cannot
  distinguish the same sender's direct conversation and several groups in the domain.
- `message_content.go`: sealed, typed content and `SendableContent` already separate received and
  sendable kinds. `InteractiveContent` is inbound text only. `engine/route.go` matches visible
  text, including fuzzy matching; a button value is not a distinct deterministic input today.
- `adapters/sqlite/sqlite.go`: contacts are unique by `(scope, phone)`, chats by
  `(scope, bot, contact_id)`, messages by `(chat_id, provider_message_id)`, and accounts hold `jid`.
  `adapters/sqlite/stores/turn.go` registers alternate phone recipients and queues their address.
- `fleet/outbox.go`: every send error retries until the attempt budget is exhausted, including
  unsupported content. Success means `MarkSent`; there is no delivery-receipt pipeline.
- `config/reload.go` compares existing channel settings, not a channel selector or account mode.
  Publication, saved/active versions, masking, and scope-isolated activation already exist.

These are semantic changes, not a rename of `Phone` to `ID`. Do not use phone columns as an
undocumented container for unrelated provider IDs.

## 3. Reference cases that constrain the design

### Telegram: three different account modes

1. **Bot API bot:** provisioned outside Botflota and authorized with a bot token. `getMe` validates
   that token and returns its bot identity [T1]. This is not login to the person's Telegram account,
   has no universal QR step, and does not let the bot message any arbitrary person by phone.
   Updates use polling or webhooks, not both; pending updates are kept for at most 24 hours [T1].
2. **Connected account:** a business-enabled bot can receive and answer permitted conversations on
   behalf of an account after the person authorizes it in Telegram. The connection has its own ID,
   enabled state, and rights; permission and reply-window checks remain dynamic [T2]. A host can
   guide this approval and Botflota can observe it, but a bot token alone does not identify the
   connected account. A shared service bot would need explicit dispatch and ownership design;
   preparation must not silently make one credential equal one independently owned account.
3. **User client:** MTProto authorizes a user account using supported login methods, including QR
   or verification code and possibly a 2FA password [T3–T5]. QR tokens expire and rotate; login can
   require data-center migration. A third-party client cannot assume SMS is always available.
   This is a different adapter/security decision, not another parameter of a Bot API bot.

Telegram identifies users and conversations separately. Message IDs are conversation-local;
callbacks have their own query IDs and must be answered promptly with `answerCallbackQuery` [T1].
Ordinary Bot API access must not claim MTProto history, user credentials, or human-reply visibility.
Buttons supported by a provider are not automatically supported by every adapter/account mode.

### SimpleX Chat: profile ownership and connections, not phone login

The documented bot integration connects to a separately running `simplex-chat` CLI over WebSocket.
Commands have `corrId`; events do not necessarily have one. Its API has no built-in authentication
or transport encryption and binds locally [S1]. A browser must never access that endpoint directly.

The API can create a local user profile, configure it as a bot, create a contact address, and
configure connection acceptance [S2]. The profile's database and keys are its runtime identity.
The contact address, even when displayed as a QR, invites contacts; it is not an account-login QR.
Contact/chat IDs are local to the managed database/profile namespace [S3]. Display names are not
identity keys, and two unrelated profiles must not merge contacts with the same local number.

`APISendMessages` schedules background network work and returns chat items; delivery status arrives
separately [S2–S4]. Command acceptance must not become a claim of recipient delivery. Command menus
are documented [S1]; do not advertise Telegram-style callback buttons without a verified equivalent.
Existing mobile-profile authorization or simultaneous use through another client remains unproven
by the bot API references: require a separate supported-flow investigation before promising it.
The FAQ documents mobile-to-desktop linking on the same network [S5]; that is not evidence of an
independent, always-on server login to the mobile profile. Do not substitute a contact QR for it.

## 4. Required contract versus optional features

### Mandatory baseline for a registered channel

- Identify its messaging provider, adapter kind, and account mode separately. Bot identity remains
  `BotRef{Scope, Bot}`; a provider is not a config namespace or an API credential.
- Support inbound and outbound plain text in an authorized direct conversation. Guarantee neither
  unsolicited outreach nor arbitrary-address delivery. Validate a usable sender and reply target.
- Keep the four transport operations. `Connect` starts operation using established credentials,
  never interactive setup; readiness and eventual connection must not be inferred from its return
  alone. `Disconnect` must unblock readers/workers and respect its bounded shutdown contract.
- Define `Send` success as acceptance by the documented downstream transport/queue, not recipient
  delivery or read confirmation. Keep the optional provider ID and at-least-once crash semantics;
  a timeout may have an uncertain outcome and must not be presented as a proven rejection.
- Specify instance lifetime, channel ownership, concurrency, and cancellation: one actor reads
  inbound; the outbox sends concurrently; the adapter owns channel closure. A stopped instance is
  not reused unless its contract explicitly permits it. Clean up partially started instances.
- Preserve bounded buffering and explicit backpressure. Document upstream acknowledgement/replay
  boundaries; putting a message on `Incoming` is not proof its turn committed. Do not add a durable
  inbox blindly, but settle its necessity before advancing an irreversible update cursor.
- Normalize IDs without losing provider-local namespaces, preserve content/timestamps, and supply
  stable inbound IDs where available. Declare missing replay/dedup guarantees rather than inventing
  random IDs or claiming exactly-once delivery. Wire structs and keys stay inside adapters.
- Provide sanitized account metadata, readiness, supported setup methods, declared capabilities,
  and explicit operation errors to the control plane, without adding them all to `MessageChannel`.

### Optional and independently advertised

Media subtypes, location, contact cards, polls, response buttons, URL buttons, formatting, groups,
topics, typing, marking inbound read, delivery receipts, history import/backfill, address-book
import, message editing/deletion, and external-reply observation are not baseline obligations.
Neither QR nor a credential-submission form is mandatory for every account mode.

Reuse the existing optional ports where their semantics still fit. Add a small operation interface
only when a runtime consumer exists. Sending optional content still uses `Send`; do not add a
`SendX` method per content type. Poll creation and poll-vote input are different capabilities.
Manual API chat holds do not require `OutboundObserver`; automatic external-reply detection does.
Transport status reporting can stay optional, but absence must be shown as unknown, not connected.

## 5. Identity, conversations, and stored addressing

Propose two separate values, validated at the adapter boundary:

- **Contact key:** provider + identity namespace + opaque external subject ID. Scope participates
  in every durable lookup. A provider-wide identity may be shared by bots in one scope; a local
  identity includes the owning profile/database namespace. Phone is optional metadata, not fallback
  identity. WhatsApp can keep PN normalization inside its adapter and keep its existing contacts.
- **Conversation target:** owning account binding + conversation kind + opaque external thread ID,
  with a topic/subthread component only when needed. An inbound envelope carries both sender and
  conversation; a reply defaults to that conversation, never the last conversation cached by sender.

Namespace identifiers must survive reconnect/restart and must not depend on display names, ports,
or transient channel instances. Use structured comparable keys, not ambiguous concatenated strings.
API representations should use opaque string IDs, not JavaScript-rounded integers or JIDs.
Never infer the same person across providers, or across unrelated SimpleX profiles.

Changes to plan together:

1. `message.go` and `OutboundMessage`: replace phone-only routing with the two values; distinguish
   message identity from interaction/event identity. `SendRecord`, `OutboundRecord`, and
   `OutboxEntry` must persist the exact target and account binding, not rebuild it from a cache.
2. `Contact`, `Chat`, stores, and DDL: keep internal numeric IDs, add neutral external keys, and key
   chats by conversation rather than sender alone. A stored message needs its sender when a shared
   conversation is supported; a group must not be materialized as a person's contact.
3. Engine/filter/control/templates: resolve contacts by subject and sessions/holds/guards by chat.
   Add neutral contact/message/bot context values; retain `contact.phone` only for real phone data.
   Define unavailable optional fields without weakening missing-path errors (ADR 0018).
4. Alternate `to`: identify a target in the current bot's channel/account, with adapter validation
   after rendering. Never choose another provider/account implicitly. Preserve and document the
   existing triggering-chat history convention, or change it explicitly with provenance tests;
   provider IDs written onto such replies must use the actual destination's dedup namespace.
5. History/dedup: distinguish bot/account namespace, external conversation, message ID, and event
   kind. A reused account-local ID or a callback referring to an existing message is not a duplicate
   text turn. Transport wire anchors for history remain opaque to the engine.

Preparation must establish safe conversation addressing; it need not implement group graph policy.
Until participant, session, and guard semantics are decided, unsupported shared conversations must
be refused or filtered explicitly, not processed as direct messages. Test the same sender in two
conversations so the abstraction cannot regress to sender-only storage.

## 6. Capability discovery and validation

Use a small immutable, injected catalog of installed channel definitions, assembled at the
composition root. It supplies provider/account modes, pure config/content validation, capabilities,
and builders. `config` consumes declarations without importing adapters or `engine`; the core keeps
standard-library-only contracts. Do not build runtime plugins, reflective registration, or `init`.

Describe **implemented** send/receive content and feature variants, not merely platform support.
`interactive=true` is insufficient to distinguish reply buttons, links, menus, and selection input.
Relevant limits use provider-correct units: bytes for callback data, characters for some text,
bytes for media. Do not silently split one durable message into several wire messages while `Send`
and the outbox still record only one ID.

Keep three answers distinct: supported by this adapter, authorized for this account, and currently
usable for this conversation. Dynamic rights, connection acceptance, block state, or reply windows
cannot be certified by offline YAML validation. Unknown permission is not permission granted.
API discovery must expose sanitized declared support and effective restrictions without secrets.
Propose `GET /api/v1/scopes/{scope}/bots/{bot}/channel` for that description, including the account
mode and which optional operations exist. A disconnected bot still has declared support, but no
invented current permission. Never enumerate other scopes' bindings or managed endpoints.

Validation has three points, using the same vocabulary and pure rules:

1. Load/publication: check channel kind and typed options; examine each bot's referenced graph,
   entry/exit actions, tools, and guard `on_blocked` steps against declared support. Shared actions
   are checked in the context of each consuming bot, not globally against one provider.
2. Execution/API submission: validate rendered content and target before queuing, and choose any
   explicitly configured fallback before actions or HTTP side effects that depend on that choice.
3. Delivery: recheck dynamic eligibility; reject unsupported/permanently invalid input immediately,
   retry transient errors within bounds, and honor provider retry hints when supplied.

Never silently drop a feature or downgrade a message. Default to rejection; a textual fallback is
author-declared and must keep the same intended choice values. A known unsupported configuration
fails validation, and legacy queued unsupported messages become failed without exhausting retries.
Dynamic loss of account authorization must be visible and must not make old queued messages send
from a replacement account. Settle pause-versus-failure behavior at the decision gate.

## 7. Remote account setup and authorization

### Separate the three authorities

The host's caller identity, the owner/scoped credential authenticating Botflota API requests, and
the credential authorizing a messaging account are unrelated. Provider account setup belongs under
that bot's account resource; keep the CLI `auth` namespace reserved as ADR 0025 requires.
Every registered account mode needs a usable setup or managed-binding path, but the transport
must not pretend to implement every login method.

Define a narrow account-lifecycle port beside the transport: inspect readiness, describe supported
setup methods, and detach Botflota's binding. Interactive setup uses a separate session runner.
Reuse current per-bot locks and completion waits. Pairing, enrollment, credentials, connection
state, and history-import progress become separate facts; importing history cannot define success.
Readiness must distinguish credential presence from provider authorization and transport health.
Return an actionable condition such as unconfigured, configured, action-required, or unavailable,
plus safe reasons and the appropriate setup method; an existing credential is not proof of access.

### Session and next-step model

Propose an account setup session containing `BotRef`, session ID, method, account mode, state,
revision, start/update/expiry times, sanitized error code, and an optional typed next step.
States cover starting, awaiting input, awaiting provider approval, succeeded, failed, cancelled,
and expired. Report successful account persistence separately from runtime activation failure.

Use a small closed set of step forms, not provider commands or a general form/schema engine:

- Display an expiring QR/login URI; its payload expiry is separate from the session deadline.
- Request a credential, identifier, verification code, or password as a typed input. Secret values
  are write-only; a password is a step, not a reusable account credential to publish or retain.
- Request an external action using a safe destination/instruction code, then await a provider event.
- Select/create a managed profile through an approved method with bounded, typed parameters.

A QR is presentation, not a universal auth method: tag login challenges separately from public
contact invitations. Inputs bind to session ID, current step ID/revision, expected input type, bot,
and scope. A stale page cannot submit a response to a replacement session or account. The host
renders known steps and may decline an unsupported step; it never runs arbitrary adapter commands.
Methods that cannot complete remotely must say so, with a supported external action or clear error.

### Proposed API surface

Let `B` mean `/api/v1/scopes/{scope}/bots/{bot}`; these routes do not exist yet:

| Operation | Proposed route | Result |
| --- | --- | --- |
| Inspect binding/readiness | `GET B/account` | Safe identity and lifecycle metadata |
| Discover setup | `GET B/account/methods` | Available methods and input requirements |
| Begin setup | `POST B/account/enrollments` | `202`, session ID and first state/step |
| Follow setup | `GET B/account/enrollments/{id}` | Current state, revision, expiry, step |
| Supply next input | `POST B/account/enrollments/{id}/inputs` | Validate step; return safe state |
| Cancel setup | `DELETE B/account/enrollments/{id}` | Idempotent cancellation |
| Forget binding | Existing `DELETE B/account` | Stop bot and remove its local binding |

Prefer additive routes and a WhatsApp-only translation of existing `/pairing` responses so current
CLI/API callers do not break. Fix error/status mappings before implementation: 404 for unknown or
out-of-scope targets, 409 for an incompatible active session or stale step, bounded 429 for retries,
and an explicit unsupported-method result. A malformed method is not an upstream internal error.
Retain the current polling option; SSE and new lifecycle webhooks are separate roadmap work.

For example, an input request supplies `step_id`, `revision`, and a typed code/token/password value.
Its response never echoes that value. GET exposes only a display challenge or masked input hint.
Support safe retries after lost HTTP responses: a start request key returns the same session, and
an input request key must not submit the same credential/code twice. Keys are scoped to the bot and
session; a conflicting request is rejected. GET/DELETE by stale session ID cannot affect a new one.

### Lifetime, concurrency, and custody

- A setup worker belongs to the instance, not the POST request's context or the web page lifetime.
  One setup mutation per bot runs at a time; different bots remain independent. Bound its timeout,
  retries, and retained completed sessions; stop and await workers before closing stores.
- For the first implementation, active sessions can remain in memory as today. A process restart
  requires a fresh session; persisted account success is discovered through `GET B/account`.
  Do not persist OTPs/passwords for resumability or claim an active session survived restart.
- Stage credentials until provider confirmation and local account commit. A cancelled/expired
  uncommitted session cannot activate later. Once committed, cancellation cannot claim the binding
  never existed; explicit detachment removes it. Unpair waits for the worker's actual completion,
  including the existing successful-pairing/history-wait race. Provider authorization and local
  persistence are not one transaction: define cleanup or an explicit recovery condition if provider
  success is followed by a local commit failure, rather than orphaning a live remote session.
- Channel credentials are server-side, adapter-owned runtime state, not graph context or draft
  YAML. Reusable provider credentials cannot be stored only as hashes: unlike API bearer-token
  verification, the channel must use them to authenticate. Decide protection at rest, backup/key
  custody, and recovery before storing new credential types; `spec.Secret` masks, not encrypts.
- Never return session databases, private keys, bot tokens, passwords, or OTPs on reads, history
  export, errors, logs, or future event streams. Challenges use authenticated, non-cacheable reads;
  inputs travel in bounded request bodies over protected transport, never query strings.
  Sanitize provider errors and URLs, including Telegram's token-bearing request URLs.
- Scope access is checked before reading setup/account data. Session IDs and QR possession are not
  API authorization. Current scoped tokens have full scope authority; do not invent per-person
  ownership or a new role. The host enforces narrower application permissions server-side.
- Detachment removes Botflota's local binding and settles pending sends without deleting history.
  Disconnect, forget-local-credentials, provider-session revocation, contact-address deletion, and
  account/profile destruction are different operations. The common delete must not destroy a
  SimpleX profile or reset every Telegram session; stronger operations require explicit support.

Reauthorization must also be remotely operable. A revoked/expired account may enter a new setup
session without deleting its conversations. Renewing the same provider identity preserves its
identity namespace; switching identities is explicit replacement, not credential rotation.
Serialize renewal with sends, verify the returned identity, and settle old pending messages before
advancing the binding generation. If renewal/replacement fails, report the actual remaining binding
and readiness; do not claim automatic rollback of provider-side authorization. Other bots continue.
Preserve legacy `/pairing` conflict behavior through its translation, not by requiring every new
account mode to be unpaired before it can repair authorization.

### How the reference modes would consume this contract later

- WhatsApp: display QR, observe approval, commit device binding, optionally import history, then
  activate. Preserve current QR confidentiality and PN/LID handling inside its adapter.
- Telegram Bot API: receive a write-only token, validate with `getMe`, persist the binding, then
  activate. Invalid tokens fail setup; no phone/OTP/QR is invented.
- Telegram connected account: guide provider-native approval and await the verified connection
  identity/rights. Bind that identity to the intended bot/scope using proven correlation, not an
  unchecked browser-supplied connection ID. Shared transport dispatch is a separate design gate.
- Telegram user client: publish supported QR or identifier/code/password steps while protocol
  state, API application credentials, and DC migration remain server-side. No custom SRP code in
  the core, browser session imports, account recovery, or automatic account creation is implied.
- SimpleX: select an owner-approved managed endpoint/profile or create a profile through supported
  operations, verify its identity, establish the binding, and expose its contact invitation safely.
  Do not let a scoped caller choose a server path, raw database, arbitrary WebSocket URL, or other
  profile. Infrastructure and endpoint approval belong to the owner; an existing profile must be
  bound without implying permission to delete it. Multiple profiles/consumers require isolation and
  command-routing proof, not mutable active-user switching per incoming message.

## 8. Configuration, persistence, and compatibility work

- Add a typed channel selector/account mode to `spec.Bot`; keep the existing missing-selector
  configuration equivalent to the current WhatsApp adapter during a documented transition.
  Reject an unavailable adapter, even if its name appears in the reference cases above.
- Keep common pacing/filter settings separate from adapter-specific options. Presence/history
  settings are opt-in capabilities, not required simulation behavior. Explicit unsupported settings
  fail; existing implicit WhatsApp defaults must not silently enable features on another channel.
- Inject the same catalog into CLI validation, startup, watcher loads, and API publication. Preserve
  strict decoding, origin-aware errors, scopes, masking, saved/active versions, and atomic writes.
  Regenerate `config.schema.json` from Go types only when those types are implemented.
- Remote credentials use account setup, not publication. Decide whether a local reference-based
  credential method is necessary; if accepted, extend ADR 0014 and its validation/masking rules
  deliberately. Publishing config must never overwrite a runtime credential with `***`.
- `config/reload.go` must compare channel kind, account mode, adapter options, and binding-relevant
  references. Keep transport changes restart-required initially; do not reconnect another scope.
  A provider/profile change is an identity change, not a label edit or silent history migration.
- Replace JID-only account metadata with provider/mode, stable account namespace, safe identity,
  and a binding generation. Keep actual device/session material in adapter stores. Queue entries
  must remain tied to the account authorized when accepted, never just `(scope, bot)` after relink.
- Generalize contact/chat/address-book SQL and all corresponding store signatures together.
  Preserve foreign keys, first-contact semantics, atomic turns/API sends, guards, and the outbox.
  A local ID namespace and source/destination conversation identifiers must round-trip on restart.
- Decide the data transition before changing DDL. Today the schema is applied in place with two
  additive column upgrades, not a versioned migration runner. Do not prescribe dropping existing
  databases. An accepted upgrade must map WhatsApp contacts/accounts/chats without losing numeric
  IDs, sessions, dedup keys, outbox targets, or credentials; test against a pre-change database.
- Evolve API reads and selectors additively: expose neutral contact/account/chat references and
  retain legacy phone selectors only for WhatsApp. Resolve numeric/opaque chat IDs under their
  scope and bot, never by ID alone. CLI query/control commands must use the same neutral selectors.
- First-contact webhooks and CSV exports need neutral identity/provenance without leaking keys.
  Keep existing fields where possible and settle incompatible payload/archive changes explicitly.
  History migration must reject incompatible providers/local namespaces until an explicit mapping
  exists; moving unreadable SimpleX local targets cannot turn them into valid targets of a new bot.
- Do not rely on file-root, egress, or scope-secret protection being implemented: ADRs 0047–0049
  are accepted but their deliverables remain unchecked. A managed local channel endpoint is not an
  arbitrary action URL; decide its separate trusted connection boundary before adding one.

## 9. Optional interaction extension, without premature implementation

Keep typed content; do not replace it with raw provider JSON or an unvalidated `map[string]any`.
The extension recipe for the first real button consumer is: add a neutral outbound choice payload
with labels and stable values, a distinct inbound selection payload, JSON codecs, advertised
variants/limits, and deterministic graph input handling. URL buttons are not response selections.
Preserve current stored `InteractiveContent` decoding or supply an explicit compatibility codec.

A selection carries an event ID, actor, conversation, source-message reference, and choice value.
Validate its association, current graph/session eligibility, and replay/expiry behavior before
side effects. It must not reach fuzzy matching or an LLM as if its label were user-written text.
Adapter-level callback acknowledgement is distinct from graph acceptance and outbox pacing.
A poll vote or edit is not automatically a new turn; ADR 0036 currently discards control traffic.

Preparation records and tests this boundary with fakes; it need not ship an unused button DSL,
`Editor`/receipt port, full event bus, or menu fallback interpreter. Implement each extension with
its first actual consumer and decision, not a method returning unsupported in every adapter.

## 10. Decision gates and delivery slices

The gate is closed (2026-10). The frozen decisions live in ADR 0051–0054, amending the
canonical ADRs where needed:

1. Neutral identity and conversations: ADR 0051 (ADR 0020, 0026, 0027, 0029, 0037, 0042, 0043,
   0044). Data transition: additive columns with a WhatsApp backfill, preserving numeric ids,
   sessions, dedup keys, and queued sends, tested against a pre-change database.
2. Account setup and custody: ADR 0053 (ADR 0004, 0021, 0022, 0025, 0028, 0039, 0046). Credentials
   follow the device store's trust model; an explicit encryption and key-custody decision is
   required before the first non-WhatsApp credential type.
3. Capabilities and delivery: ADR 0052 (ADR 0011, 0036, 0040, 0044). A permanent rejection fails
   without consuming retries, transient errors retry, losing the same binding's authorization
   stops the bot with its queue pending, and a binding replacement fails the old entries.
4. Configuration and compatibility: ADR 0054 (ADR 0001, 0006, 0007, 0009, 0014). A missing
   selector means WhatsApp native during the transition; transport changes stay restart-required
   and never reconnect another scope.

P0 is complete: the reference cases in §3 and §7 map to the frozen contracts — a WhatsApp account
with QR setup and PN subjects, a Telegram bot token as a write-only setup input, a Telegram user
login with QR or input steps and no universal QR, and a SimpleX managed profile with a local
namespace — none needing a fake phone, a universal login QR, a leaked credential, or a global
address. P1–P6 are tracked in `../PLAN.md` Phase 7.

Deliver one coherent slice at a time; every implementation slice must keep WhatsApp runnable:

- **P0 — Decisions and contract fixtures (done, 2026-10).** The frozen shapes, states,
  compatibility strategy, and mandatory/optional matrix live in ADR 0051–0054; the
  representative WhatsApp, Telegram-token, user-login, and local-profile cases are described in
  §3 and §7 without a fake phone, universal QR, leaked credential, or global-address assumption.
- **P1 — Identity and target propagation.** Change `message.go`, `records.go`, store ports, SQLite
  schema/stores, engine, fleet filters/holds, and WhatsApp event/send/import mapping as one topic;
  adapt existing API/CLI consumers or retain explicit compatibility translations so all compile.
  Gate: legacy direct chats survive the approved upgrade; same sender in different conversations
  has independent state; a target and its account namespace survive a durable outbox restart.
- **P2 — Declared capabilities and channel configuration.** Add the injected catalog, typed bot
  channel configuration, pure support checks, reload comparison, and selective composition in
  `cmd/botflota/fleet_cmd.go`. Gate: old YAML works; one fake text-only channel requires no WhatsApp
  device store; unsupported graph/settings/content fail at the correct boundary.
- **P3 — Account setup runtime.** Replace JID/pairing-only assumptions with neutral metadata and
  narrow account/setup ports; adapt the WhatsApp manager and startup readiness/composition without
  moving protocol code into core or inspecting every configured bot through WhatsApp's store.
  Gate: QR, write-only credential, multi-step approval, and profile binding are modeled by fakes;
  cancellation/commit/shutdown races preserve the actual stored result and never activate twice.
- **P4 — Remote API and CLI integration.** Generalize `api/api.go`, pairing/account handlers,
  views, params, sends, history operations/export, and operational CLI consumers; expose capability
  and setup discovery and preserve the approved legacy route translations. Gate: a scoped HTTP
  client completes setup and handles reauthorization without filesystem access; out-of-scope
  reads/inputs/cancellation are opaque; personal CLI uses the same operations while online.
- **P5 — Delivery and optional-operation semantics.** Update outbox error classification, account
  generation checks, and normalized history/external-reply contracts; add interaction contract
  fixtures, not a live Telegram/SimpleX adapter. Gate: permanent errors do not block FIFO with
  useless retries, transient sends retry, acceptance is not delivery, and a replacement account
  cannot send the previous account's pending entries.
- **P6 — Regression gate and documentation.** Align glossary, architecture, API/integration docs,
  relevant guides/ADRs, config examples/schema, and accepted `PLAN.md` deliverables with code.
  Gate: `make check`, `make test-race` for concurrent changes, and the WhatsApp manual checklist
  pass in the main repository. No successful build/test may be inferred from a design review.

Use table-driven `t.Run` tests and hand-written fakes. Essential cross-cutting cases include:
same external ID in two providers/profiles/scopes; reconnect with stable namespaces; unknown or
stale setup inputs; QR rotation versus session expiry; lost-response retries; account commit then
activation failure; revoked rights; own-send echo versus human reply; suppressed inbound recording;
alternate-recipient dedup provenance; incompatible history migration; invalid publication preserving
the active channel; explicit fallback preserving choice values; and credential redaction in every
JSON/log/error/export path. Test limits and slow/cancelled producers without real network services.

Preparation is complete when the existing WhatsApp adapter and contrasting fake channels run
through the same runtime/control plane, with meaningful identities, honest capabilities, and remote
account setup. It is not complete merely because another struct satisfies four methods. Actual
Telegram/SimpleX integrations, dependency selection, provider-specific login spikes, connected-bot
dispatch, and approval of SimpleX's auxiliary-process deployment remain separate follow-up work.

## References

Official references inspected on 2026-10-07; provider eligibility and API versions must be rechecked
when an adapter starts. The SimpleX `stable` documentation is mutable, not a pinned SDK guarantee.
Internal ownership stays with the linked documents and ADRs, not duplicated decisions in this plan.

- [T1] [Telegram Bot API](https://core.telegram.org/bots/api): authorization, updates, callbacks.
- [T2] [Connected business bots](https://core.telegram.org/api/bots/connected-business-bots).
- [T3] [Telegram user authorization](https://core.telegram.org/api/auth).
- [T4] [Telegram QR login](https://core.telegram.org/api/qr-login).
- [T5] [Telegram 2FA](https://core.telegram.org/api/srp).
- [S1] [SimpleX bot API](https://github.com/simplex-chat/simplex-chat/blob/stable/bots/README.md).
- [S2] [SimpleX commands][simplex-commands]: profiles, addresses, and `APISendMessages`.
- [S3] [SimpleX types][simplex-types]: `User`, `Contact`, `ChatRef`, `CIStatus`.
- [S4] [SimpleX events][simplex-events]: `NewChatItems`, `ChatItemsStatusesUpdated`.
- [S5] [SimpleX FAQ](https://github.com/simplex-chat/simplex-chat/blob/stable/docs/FAQ.md).

[simplex-commands]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/COMMANDS.md
[simplex-types]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/TYPES.md
[simplex-events]: https://github.com/simplex-chat/simplex-chat/blob/stable/bots/api/EVENTS.md
