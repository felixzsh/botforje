# ADR 0053 — Remote account setup and credential custody

Status: Accepted (2026-10)

## Context

Pairing is the only account setup Botflota has: QR-only, in memory, and
WhatsApp-specific. The API requires linked-account, pairing, and address-book
dependencies even for a channel that has none; `PairingRunner` fixes the
method to `qr` and its session carries a `Code`; and `LinkedAccount` parses a
JID in the core. A remote caller — a host's web interface — must configure an
account without shell access, and another provider has a different setup: a
bot token validated once, a user login with a code and a password, or a
managed local profile. A QR is presentation, not a universal method.

## Decision

**A narrow lifecycle port beside the transport.** `AccountLifecycle` inspects
readiness and detaches Botflota's local binding. `SetupRunner` describes supported
methods and drives interactive `SetupSession` instances. `MessageChannel` keeps
its four operations, and the optional ports stay optional (ADR 0004). WhatsApp
QR setup implements the same contracts; the old `PairingRunner` surface is
removed with its P4 consumers, not retained as a compatibility translation.

**Readiness separates facts.** Credential presence, provider authorization,
and transport health are different facts. Readiness answers with an actionable
condition (`unconfigured`, `configured`, `action_required`, `unavailable`), safe
reasons, and the appropriate setup method. An existing credential is not proof
of access.

**Three versions stay distinct.** A setup session's `revision`, a binding's
`generation`, and the active config snapshot of ADR 0009 are independent
values: none substitutes for another, and a change in one never implies a
change in the others.

**P2 implements only the join decision.** A channel definition reports whether
one bot can join the fleet now and a safe state string; startup skips an
unready bot without stopping its peers, and an adapter's stores are opened only
when the config selects it. The session, revision, and generation model below
is this ADR's decision and is not implied by that hook.

**Session and steps.** A setup session carries `{bot ref, session id, method,
account mode, state, revision, start/update/expiry times, sanitized error code,
optional typed next step}`. States are `starting`, `awaiting_input`,
`awaiting_approval`, `succeeded`, `failed`, `cancelled`, and `expired`. Steps
are a small closed set, not provider commands or a general form engine:

- display an expiring QR or login URI, whose payload expiry is separate from
  the session deadline;
- request a typed input (credential, identifier, verification code, password);
  secret values are write-only, and a password is a step, never a reusable
  account credential to publish or retain;
- request an external action through a safe destination or instruction code
  and await a provider event;
- select or create a managed profile through an approved method with bounded,
  typed parameters.

Account persistence success is reported separately from runtime activation
failure.

**Neutral routes (owner decision 2026-10-12).** Let `B` be
`/api/v1/scopes/{scope}/bots/{bot}`. Public names follow the domain's `Binding`
and `SetupSession`, not a second account/enrollment vocabulary (ADR 0054).

| Operation | Route | Result |
| --- | --- | --- |
| Inspect binding and readiness | `GET B/binding` | Binding and safe readiness |
| Discover setup | `GET B/binding/methods` | Methods and input requirements |
| Begin setup | `POST B/binding/setups` | `202`, setup session |
| Follow setup | `GET B/binding/setups/{id}` | State, revision, expiry, step |
| Supply next input | `POST B/binding/setups/{id}/inputs` | Safe setup session |
| Cancel setup | `DELETE B/binding/setups/{id}` | Idempotent cancellation |
| Forget binding | `DELETE B/binding` | Stops the bot, removes local binding |

P4 removes the old `/account` and `/pairing` routes and the proposed
`enrollments` spelling. It exposes no aliases or compatibility translation.
The CLI uses `bot bind` and `bot unbind` over these same operations, including
its locked offline path; QR is a display step, not a separate public command.

The inspection JSON is `{binding, condition, reasons, method}`: an absent
association is `binding: null`, and a present binding carries `scope`, `bot`,
`provider`, `adapter`, `mode`, `namespace`, `identity: {provider, subject}`,
`generation`, `created_at`, and `updated_at`. It exposes no SDK locator or
credentials. `condition` uses the readiness values above. `reasons` are safe
facts, including explicit unknown authorization or transport health; configured
never means authorized or connected. Declared channel discovery is ADR 0052.

A setup session uses `bot: {scope, bot}`, `id`, `method`, `mode`, `state`,
`revision`, `started_at`, `updated_at`, `expires_at`, `error_code`,
`activation_error`, and optional `step`, following P3's neutral types. Activation
failure is sanitized and separate from binding success. Begin uses `method`,
`request_key`, and adapter-validated `params`; input uses `revision`,
`request_key`, and write-only `values`. Setup IDs never refer to graph runs.

Status mapping is fixed: 404 for an unknown or out-of-scope target, 409 for an
incompatible active session, conflicting request key, or stale step revision,
a bounded 429 for retries, and 422 `unsupported_operation` for a method or
operation the selected definition does not implement. Malformed inputs are
client errors, never upstream internal ones. Authorize before discovery or
session lookup; challenges are authenticated and non-cacheable. Responses,
logs, and exports never echo a submitted secret, and reads expose only safe
step metadata and display challenges. Bound input bodies and retry budgets.

**Idempotent retries.** A start request key returns the same session; an input
request key must not submit the same credential or code twice. Keys are scoped
to the bot and session, and a conflicting request is rejected. A stale session
id cannot affect a replacement: GET and DELETE by an old id are inert.

**Lifetime and custody.** A setup worker belongs to the instance, not to the
POST request or the web page. One setup mutation per bot runs at a time, and
different bots stay independent. Workers, retries, timeouts, and retained
completed sessions are bounded, and workers stop before stores close. Active
sessions stay in memory for the first implementation: a restart requires a
fresh session, and persisted account success is discovered through
`GET B/binding`. No OTP or password is persisted for resumability.

Credentials are server-side, adapter-owned runtime state — never graph context,
draft YAML, or an API read. The native WhatsApp adapter uses the shared SQLite
device store; a managed auxiliary service may own its protocol keys and
sessions in its own store. Common binding persistence records the neutral
association, not a copy of every SDK's credential schema. This does not change
Botflota's SQLite persistence decision (ADR 0006) or authorize arbitrary
external stores/endpoints; trusted auxiliary deployment still needs ADR 0054's
decision. Reusable provider credentials cannot be stored as hashes alone,
because the channel must use them to authenticate. Protection at rest follows
the device store's existing model
(instance lock, file permissions, no exposure); an explicit encryption and
key-custody decision is required before the first non-WhatsApp credential
type ships. Session databases, private keys, tokens, passwords, and OTPs never
appear in reads, history exports, errors, logs, or future event streams.
Session ids and QR possession are not API authorization: scope access is
checked first.

**Commit and cancel.** Credentials are staged until provider confirmation and
local account commit. A cancelled or expired uncommitted session can never
activate later. Once committed, cancellation cannot claim the binding never
existed; explicit detachment removes it. Unbind waits for the worker's actual
completion, including the successful-pairing and history-wait race. Provider
success followed by a local commit failure gets a defined recovery condition
instead of an orphaned live remote session.

**Detachment is local.** `DELETE B/binding` removes Botflota's local binding
and settles pending sends without deleting history. Disconnect, local
credential removal, provider-side session revocation, contact-address
deletion, and account or profile destruction are different operations; the
common delete must not destroy a SimpleX profile or reset every Telegram
session, and a stronger operation needs explicit support.

**Reauthorization is remote too.** A revoked or expired account enters a new
setup session without deleting conversations. Renewing the same provider
identity preserves its namespace; switching identities is an explicit
replacement. Replacement is serialized with sends, verifies the returned
identity, settles the old pending messages before the generation advances,
and reports the actual remaining binding on failure; other bots continue.

Temporary selection of a backend's execution profile is neither setup nor
replacement and does not advance binding generation. Detaching a logical view
must not stop peer views or destroy another profile in a shared service.
Retire the old binding's observations before activating a replacement, rather
than treating buffered events as traffic from the new account (ADR 0051).

## Consequences

- A scoped HTTP client completes setup and reauthorization without filesystem
  access; QR, token, multi-step approval, and profile binding are modeled by
  the same session contract.
- WhatsApp's QR confidentiality and PN/LID handling stay inside its adapter.
- The `auth` CLI namespace stays reserved for Botflota credentials (ADR 0025);
  setup stays under the bot's binding resource.
- ADR 0004, 0021, 0022, 0025, and 0028 are updated in place to this contract.
  P3's runtime is implemented; P4's public contract is approved, with code and
  gates still pending in `docs/channels/phases.md`.
