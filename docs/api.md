# REST API contract

This document defines the API's scope coverage and authority model. It is not
the generated endpoint reference; the endpoint reference follows the code.
`architecture.md` documents how the running instance composes and serves it.

## API contract

The target is for the API to be the control plane for host- and user-initiated
operations on a running instance. Every capability that reads or changes
scope-owned configuration, bots, conversations, or data must be available
through the API, now and as the project grows. The operational CLI uses this
API today; the planned dashboard and host integrations use it too. None gets a
bypass around server-side authorization or direct access to the running
instance's files or database.

A scope capability is complete only when its API operation enforces authority
on the server and has tests for allowed and out-of-scope access. The API's
coverage does not mean every credential can perform every operation: the
principal's authority and its authorized scopes determine what it may do.

## Authority

There are exactly two authorities — `owner` and `scoped` — and no roles; the
pair is the complete model, now and as planned.

- A scoped access token authorizes reads and writes of the resources of its
  authorized scopes. It is not a read-only credential: inside those scopes it
  may pair and unpair bots and drive their conversations through the
  scope-addressed routes. Every request that addresses a scope checks that it
  is in the principal's authority; an out-of-scope target is
  indistinguishable from an unknown one.
- The instance owner may operate on every scope and manage instance-level
  settings, shared resources, credentials, and other instance-wide state.
  The owner may read and publish YAML for any scope.

Scope isolation is enforced by the API, not by hiding controls in the UI. A
credential authorized for several scopes can use them all, but cannot list,
read, infer, or operate on any other scope.

Scope grants are the owner's to make and the instance's to constrain. By
default the same scope may be granted to several access tokens, and each has
full authority over the scopes it holds. An instance that wants one active
credential per scope sets `exclusive_scopes` (an instance setting; changing
it requires a restart): minting then answers 409 `scope_already_granted` for
a scope another active token already holds, and a revoked or expired token
holds nothing, so its scopes can be granted again. Both behaviors are
supported and neither is imposed; the owner token covers every scope either
way. The promise has to hold for what is stored as well, so the server
refuses to start in this mode while a scope is already granted to several
active tokens; the owner resolves that overlap first.

A grant changes only by minting a token and revoking one: no renewal or
scope-grant update exists yet, and the owner operation that adds a scope to
a credential is still planned (`docs/access-policy.md`). In exclusive
mode, rotating a credential will need a coordinated replacement that leaves
no gap, which is not designed yet either.

Planned owner provisioning can create a protected limit set, a requested
number of generated-key scopes, and a scoped token authorized for those
scopes as one operation. Provisioned scopes start without a `display_name`,
so their keys serve as their initial visible labels until the owner changes
them. An empty limit set is a separate owner operation that creates neither
scopes nor a token: how many scopes a set holds is
the owner's provisioning decision, and Botflota keeps no ceiling on that
number — a host expresses its own tiers by provisioning that many scopes.
Botflota knows only complete numeric resource ceilings, not the host's reason
for selecting them. The limits belong to the set of scopes, never to an
access token or scoped-editable YAML; how large a bot's imported history may
grow is ordinary bot configuration in bytes, not a tier Botflota knows.
Rotating a token does not reset usage. A ceiling is never lowered below
current use — nothing is deleted to make a number true — and a hand-edited
file that exceeds one is refused at load without being rewritten. The catalog,
the ceilings that live in instance config instead, and storage exhaustion are
in `docs/access-policy.md`; their final enforcement contract needs an ADR
before implementation, and the whole policy may be reshaped with the host's
access model. Configuration publication and the account history operations are
separate capabilities in `docs/current-plan.md`.

## Configuration and scope operations

YAML remains the source of behavior. The target API lets a scoped principal
read and publish only its authorized scopes' configuration, including bots,
graphs, actions, and settings; the owner can read and publish any scopes. No
read-only scoped token is planned. A publication may contain one or several
**existing** scopes: every target must be authorized and carry its own
expected version; all are validated and saved atomically or none are.
Activation may still be pending per scope. Neither scoped nor owner config
publication creates scopes or edits owner policy; owner provisioning is a
separate operation (PLAN Phase 7; ADRs 0002, 0009, and 0016).

Configuration entities keep their YAML map keys as identifiers. Their optional
`display_name` is presentation metadata, not a reference or identity. A UI
shows the key when the field is absent or empty; editing that label writes the
field without renaming the key. API read models must distinguish the stored
optional field from its presentation fallback rather than writing the fallback
into YAML. Changing a key is not a display rename and must not be inferred as
one during reload. Future scope provisioning generates a unique scope key
without setting `display_name`; interfaces initially show the key, and the
owner may assign a visible name later. Manual YAML authors choose their own
keys. For UI-created child entities, the submitted initial name is the YAML
key; later renames update only `display_name`.

The server validates label uniqueness independently of the UI: effective
labels must be unique among scopes instance-wide, among members of each entity
category in a scope, and among the nodes of one graph. Equal labels across
different categories, scopes, or graphs are allowed. The effective label is
`display_name` when it says something and the key otherwise, so a missing label
cannot collide with an explicit one unnoticed. Comparison drops surrounding
space, collapses inner runs, and ignores case; only the comparison is
normalized, and the saved label keeps its text. A shared catalog joins the
rule when its types arrive (ADR 0031); config validation and every write
enforce it, so the UI only gives early feedback.

Instance-wide configuration and shared resources are owner operations. Scope
configuration does not become a second source of truth in the API or dashboard:
the API validates and publishes YAML fragments.

### Source files and publication concurrency (planned)

The API writer is not implemented yet. The API treats a scope as one logical
document, independent of how its entities are distributed across the main YAML
and nested includes. The loader already records the source file for each
entity. The writer must use that origin map to update or remove existing
definitions in their source files, including the main file when it contains
the authorized target scope. It may change only that scope and necessary
include registrations; global settings and unrelated scopes are preserved.
New definitions go to a deterministic API-managed scope fragment. If needed,
the API may update the main file's include list to make the fragment reachable.
Keeping scopes in separate include files is a user choice and a maintainability
recommendation, never a restriction on API edits.

Each requested scope carries the logical version its editor read. At commit,
the server reloads the latest include tree, applies changes only to requested
scopes in their source files, validates the complete candidate, and publishes
all affected files as one recoverable all-or-nothing change. It serializes
overlapping writes and rebases untouched scopes in shared files. If any
requested scope changed since it was read, the entire batch conflicts; no
partial save, queue, or silent retry. A token does not limit itself to one
in-flight request, so authorization is not concurrency control.

The API validates the complete candidate before writing. If the proposed
publication is invalid, it changes no files. A scoped caller receives details
for validation errors in its own scope; if loading is blocked by invalid
configuration outside its authority or by an instance-wide error, it receives
only a generic failure. The owner can inspect the full diagnostic.

Publications to different scopes merge into the active instance snapshot
without dropping another scope's update, even when their definitions share a
physical file. Re-publishing identical content is a no-op. The API uses the
same load, validate, compatibility, and scope activation pipeline as the file
watcher; it does not edit a separate in-memory YAML source.

The UI keeps drafts client-side. On a conflict it reloads the latest scope
version and asks the user to reconcile before saving again.

Bot operations address one `(scope, bot)` at a time. Pairing and unpairing
already work through the API while the instance runs; unpairing stops only that
bot, lets a send already in progress finish, and fails the rest of its queued
replies before the account is removed. Unlinking never removes history: chats,
messages, sessions, and contacts stay, so a relink finds them again. Deleting
one bot's history is a separate explicit confirmed operation that reports what
it removed, and migration moves it to another bot of the same scope for a bot
replacing a lost account; both work through the API (ADR 0042), and a bot can
export its history first so deleting it is an informed choice
(`docs/access-policy.md` holds the limits that bound them). The target adds the
remaining scope operations, including bot controls and complete configuration
editing, without stopping peer bots. Ordinary errors in one bot must remain
local to
that bot.

### Conversation control

A chat is handed to a human in two ways, both per chat: an external reply from
the linked account starts an automatic pause, and `PUT
/api/v1/scopes/{scope}/chats/control?bot=&sender=` with `{"mode":"human"}` holds
it until `{"mode":"auto"}` releases it (ADR 0043). Taking the hold cancels the
automatic replies still queued for that chat; the bot's other chats are
untouched. The pause length is the bot's `settings.external_reply_pause`, not a
request field. `GET /api/v1/scopes/{scope}/chats` reports each chat's effective
`control` (`auto`, `paused`, or `human`) and, while a pause counts down, its
`paused_till`. Control state is live runtime state: a restart returns every
chat to `auto`, and nothing received during a hold is replayed.

The `fleet run` process remains under the host supervisor. A future
owner-only dashboard/API action may request a process restart through that
supervisor; scoped authority cannot control process lifecycle. A scope
operation must not stop the whole fleet. Instance-wide changes that need a
process restart must say so explicitly.

### Outbound sends

An integration can ask a bot to message any phone of its scope, even one that
never wrote first: `POST /api/v1/scopes/{scope}/messages` with
`{"bot":"...","to":"5215594251984","body":"...","kind":"text"}` stores and
queues the message — creating the sender profile and the chat when missing —
and answers `202 {"id":123,"status":"queued"}` (ADR 0044). Only `text` is
accepted today. `client_id` is optional; repeating one already accepted
answers `409` with code `duplicate` and the accepted message's `id` beside the
error. The message is stored as `direction=out`, `ingest=api`, `origin=api`,
with no session run, and the bot's existing outbox paces and retries it like a
reply. The send does not read or change a chat's control (`auto`, `paused`,
`human`): it is delivered while a chat is held, and the channel's echo
correlation keeps it from starting a pause (ADR 0043).

### Sender reads

`GET /api/v1/scopes/{scope}/senders` lists the scope's sender profiles newest
activity first; `since` (unix milliseconds) keeps profiles whose `first_seen`
is at or after it, and an opaque `cursor` pages the list, with `next_cursor`
absent on the last page (ADR 0044). Each entry is `{phone, display_name,
first_seen, last_seen}`. `GET .../senders/{phone}` answers one profile or 404.
Senders belong to the scope, not to a bot (ADR 0029), so the route names no
bot; `GET .../bots/{bot}` reads one bot's view or 404.

### Webhooks

The instance can push events to an external system: `webhooks` in the main
config file lists `url`, `secret` (an environment reference), and the `events`
a target subscribes to (ADR 0045). Only `sender.first_seen` exists today; it
fires once per phone per scope when an allowed inbound creates the profile,
never on an import, an operator reply, or an API send. Delivery is a signed
`POST`:

    { "id": "<uuid>", "event": "sender.first_seen",
      "occurred_at": "2026-09-28T20:24:20Z", "scope": "support",
      "data": { "bot": "assistant",
                "sender": { "phone": "5215599999999", "display_name": "Ana" },
                "first_seen": "2026-09-28T20:24:20Z" } }

with `X-Botflota-Event`, `X-Botflota-Delivery` (stable across retries), and
`X-Botflota-Signature: sha256=<hex>`, the HMAC-SHA256 of the raw body under
the target's secret. Delivery is at-least-once through a durable queue with
exponential backoff; a consumer deduplicates by the envelope `id`, which every
retry reuses. Changing the webhook list requires a restart.

## Current implementation and target

Today, the API has owner and scoped bearer tokens, checks scope access on
scope-addressed routes, reads and publishes scope configuration, and supports
pairing, pairing cancellation, unpairing, exporting one bot's history as a zip
of CSVs, and holding or releasing one chat's automation (ADR 0043) while the
fleet is running. The owner-only routes expose instance
configuration and manage access tokens. YAML edits still enter through files as
well; the watcher loads and validates them and activates only safe changed
scopes (ADR 0009). The CLI uses the API for operational commands while the
instance is running.

The full target is not implemented yet: browser sessions, the complete set of
bot and conversation controls, and the embedded dashboard remain planned. Scope
publication is a Phase 7 deliverable;
the later dashboard consumes it. Scope and bot membership changes and channel
settings currently remain pending until restart; a graph topology change waits
for active sessions in its scope to expire (ADR 0009).

For direct manual edits, Botflota cannot lock out independent editors that
write files themselves. Use one editor per file and atomic file replacement;
separate per-scope include fragments let editors work on different scopes
without writing the same file. Use the API publication path for version-checked
conflict detection when it becomes available. An invalid manual edit is left
on disk; the watcher keeps the last valid active snapshot, logs the cause for
the owner, and never rolls files back. The owner can inspect and correct the
file directly or run `config validate` for detailed diagnostics.
