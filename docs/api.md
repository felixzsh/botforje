# REST API contract

This document defines the API's scope coverage and authority model. It is not
the generated endpoint reference; the endpoint reference follows the code.
`architecture.md` documents how the running instance composes and serves it.
P4's neutral public contract was owner-approved on 2026-10-12; the resource,
binding, conversation, send, and read contracts below are its implementation
target, not a claim that the new handlers already exist. `channels/phases.md`
tracks code and runtime gates; the current implementation is distinguished below.

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
  may bind and unbind bots and drive their conversations through the
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
scope-grant update exists yet. Owner grant replacement and provisioning are
TBD (`access-policy.md`), independently of aggregate policy. In exclusive
mode, rotating a credential will need a coordinated replacement that leaves
no gap, which is not designed yet either.

Aggregate resource policy, catalogs, periods, and combined provisioning are
TBD, not API contracts (`access-policy.md`). Scopes and tokens express isolation
and authority; the host owns external identity mappings and application policy
(ADR 0046; `integration.md`). Logical scope isolation is not a sandbox for
untrusted definitions; the owner can bound files, HTTP egress, and a scope's
secret names (ADRs 0047–0049). Current publication and history operations
require no limit sets.

## Configuration and scope operations

YAML remains the source of behavior. The current API lets a scoped principal
read and publish only its authorized scopes' configuration, including bots,
graphs, actions, and settings; the owner can read and publish any scopes. No
read-only scoped token is planned. A publication may contain one or several
**existing** scopes: every target must be authorized and carry its own
expected version; all are validated and saved atomically or none are.
Activation may still be pending per scope. Neither scoped nor owner config
publication creates scopes or edits owner policy; an owner provisioning API is
separately TBD (PLAN Phase 7; ADRs 0002, 0009, 0016, and 0046).

Configuration entities keep their YAML map keys as identifiers. Their optional
`display_name` is presentation metadata, not a reference or identity. A UI
shows the key when the field is absent or empty; editing that label writes the
field without renaming the key. API read models must distinguish the stored
optional field from its presentation fallback rather than writing the fallback
into YAML. Changing a key is not a display rename and must not be inferred as
one during reload. Future provisioning and its naming rules are TBD, not
an implemented generated-key contract. Manual YAML authors choose their own
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

### Source files and publication concurrency (current)

The implemented API writer treats a scope as one logical
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

The caller owns drafts and their storage. On a conflict it reads the latest
scope version and reconciles before saving again; Botflota stores no draft UI.

Bot operations address one `(scope, bot)` at a time. Pairing and unpairing
already work through the API while the instance runs; unpairing stops only that
bot, lets a send already in progress finish, and fails the rest of its queued
replies before the account is removed. Unlinking never removes history: chats,
messages, sessions, and contacts stay, so a relink finds them again. Deleting
one bot's history is a separate explicit confirmed operation that reports what
it removed, and migration moves it to another bot of the same scope for a bot
replacing a lost account; both work through the API (ADR 0042), and a bot can
export its history first so deleting it is an informed choice
(`docs/access-policy.md` holds TBD resource-policy decisions). The target adds the
remaining scope operations, including bot controls and complete configuration
editing, without stopping peer bots. Ordinary errors in one bot must remain
local to
that bot.

### P4 public resources and binding

Public names match the selected domain terminology, including CLI commands,
arguments, JSON fields, and counts, not retired Go names awaiting their rename
(ADR 0054). Let `S` be `/api/v1/scopes/{scope}` and `B` be `S/bots/{bot}`:

| Resource | Public path | Ownership |
| --- | --- | --- |
| Peer | `S/peers/{id}` | Scope |
| Directory | `B/directory` | Bot's historical snapshots |
| Chat | `B/chats` | Scope and bot |
| Graph run | `B/graph-runs` | Scope and bot |
| Channel discovery | `B/channel` | Bot's installed definition |
| Binding | `B/binding` | Bot's committed association |
| Setup session | `B/binding/setups/{id}` | Scope, bot, and setup ID |

Collections use the same names without an item ID. `peer_id` filters, where
supported, refer to a peer in the authorized scope; graph-run references use
`graph_run_id`, never an ambiguous graph `session_id`. Chat and graph-run
reads resolve their IDs under scope and bot, not by global ID alone: chat
control is `PUT B/chats/control?chat_id=<id>` with `{"mode":...}` in the
JSON body, chat sync is `POST B/chats/sync?chat_id=<id>`, and graph runs
are read from the collection as `GET B/graph-runs?chat_id=`. The chat
selector is a URL query parameter in both cases, never a body field. A graph run is
not a setup session. No `/contacts`, `/account`, `/pairing`, or `enrollments`
alias survives.

ADR 0053 owns setup discovery, begin/read/input/cancel, safe views, and status
mapping; ADR 0052 owns declared-support discovery. A binding response reports
safe association and readiness facts, not credentials. Authorization, transport
health, and target eligibility remain separate observations: missing information
is explicitly unknown, never permission. Retained history/directory reads do
not require the selected channel to implement the corresponding live import.
An absent optional operation answers 422 `unsupported_operation`, not 500.

Operational CLI names are `bot bind`, `bot unbind`, `peer`, `directory`, `chat`,
and `graph-run`; arguments name the same domain references as the API. The
common runner presents display, input, approval, and profile steps without
provider-specific commands. Submitted credentials stay write-only. While an
instance runs, operations use the API; the locked offline operator path stays
available through the same neutral contracts. See ADR 0054 for naming and
ADR 0053 for setup semantics.

### Conversation control

A chat is handed to a human in two ways, both per chat: an external observation
can start the automatic pause (ADR 0043), and
`PUT B/chats/control?chat_id=<id>` with `{"mode":"human"}` in the body
holds it until `{"mode":"auto"}` releases it. The ID selects
one conversation, not a peer's last or implied direct chat. Taking the hold
cancels automatic replies still queued for that target; other chats are
untouched. The pause length is `settings.external_reply_pause`, not a request
field. Chat views report effective `control` (`auto`, `paused`, or `human`) and
`paused_till` while a pause counts down. Control state is live runtime state:
a restart returns every chat to `auto`, and held messages are not replayed.
Chat sync uses `POST B/chats/sync?chat_id=<id>` with the same scoped lookup; it refuses
an unsupported live operation instead of guessing a target from a peer.

The `fleet run` process remains under the host supervisor. A future
owner-only dashboard/API action may request a process restart through that
supervisor; scoped authority cannot control process lifecycle. A scope
operation must not stop the whole fleet. Instance-wide changes that need a
process restart must say so explicitly.

### Outbound sends

`POST S/messages` accepts `bot` and exactly one of `chat_id` or a neutral
`target`; optional known `recipient` is an identity, not another destination.
For example, a text send to an existing chat is:

```json
{"bot":"assistant","chat_id":123,"body":"Notice","kind":"text"}
```

An explicit target has `{provider, kind, thread, topic}`; an optional recipient
has `{provider, subject}`. Values are opaque and adapter-validated, never
universal phone strings. A new destination need not have a prior inbound or a
known participant. Atomic admission creates only the necessary identity/chat
records and answers `202 {"id":123,"status":"queued"}`. It does not imply
recipient delivery or arbitrary-address outreach. ADR 0044 owns the exact
selectors, validation, and reconciliation contract.

Without a committed binding, a new send answers 409 `binding_required` before
creating data or queueing. Disconnected is not unbound. A repeat of an accepted
`client_id` reconciles with 409 `duplicate` and the stored message ID without a
new admission. Known restrictions and declared support are checked separately;
unknown authorization or eligibility never establishes a right to send.
The existing outbox owns delivery, and the API neither runs a graph nor changes
chat control. Binding-generation snapshots and delivery enforcement remain P5.

### Peer and directory reads

`GET S/peers` lists scoped peer identities, and `GET S/peers/{id}` resolves a
scope-owned peer ID, never a bare subject shared by several providers. ADR 0044
owns ordering, cursor/first-seen filters, and the neutral view. A first allowed
live inbound is distinct from registration, an API send, or an import.
`directory_imported`, `has_chat`, and `has_history` describe recorded facts,
not current directory membership, reachability, or permission.

`GET B/directory` reads that bot's retained directory snapshots (ADR 0037).
Each entry separates its `peer: {provider, subject}` from its
`source_identity: {provider, subject}`, with `scope`, `bot`, `display_name`, and
`updated_at`. No phone projection or `/contacts` alias is retained. Both reads
are available independently of the channel's current import capability.

### History export

ADR 0042 owns the approved ZIP/CSV contract: domain sections `chats`,
`graph_runs`, `messages`, and `directory`, neutral identity/target/provenance
columns, a consistent streaming snapshot, and headers for empty sections.
`GET B/history/export` exports only modeled persisted data, never credentials
or setup inputs. File naming and CSV encoding stay inside the format adapter;
shared history ports do not expose SQL table names or physical filenames.
There is no old-format reader or compatibility archive. History movement still
requires compatible provider and identity spaces; matching provider strings
alone does not grant compatibility.

### Webhooks

The instance can push events to an external system: `webhooks` in the main
config file lists `url`, `secret` (an environment reference), and the `events`
a target subscribes to (ADR 0045). Only `peer.first_seen` exists today; it
fires once per peer per scope on the first allowed inbound, never on an import,
an operator reply, or an API send. Delivery is a signed `POST`:

    { "id": "<uuid>", "event": "peer.first_seen",
      "occurred_at": "2026-09-28T20:24:20Z", "scope": "support",
      "data": { "bot": "assistant",
                "peer": { "provider": "whatsapp", "subject": "5215599999999",
                          "display_name": "Ana" },
                "first_seen": "2026-09-28T20:24:20Z" } }

with `X-Botflota-Event`, `X-Botflota-Delivery` (stable across retries), and
`X-Botflota-Signature: sha256=<hex>`, the HMAC-SHA256 of the raw body under
the target's secret. Delivery is at-least-once through a durable queue with
exponential backoff; a consumer deduplicates by the envelope `id`, which every
retry reuses. Changing the webhook list requires a restart.

## Current implementation and target

The P4 names and shapes above are implemented: neutral peer/chat/binding
references, scoped channel/binding/method discovery with the
`/binding/setups` lifecycle, committed-binding send admission, chat
control/sync under `B/chats`, collection graph-run reads, and the ZIP/CSV
export sections all serve through the routes this document describes. No
legacy selector, old route, or phone projection survives beside them.

Today, the API has owner and scoped bearer tokens, checks scope access on
scope-addressed routes, reads and publishes scope configuration, and supports
pairing, pairing cancellation, unpairing, exporting one bot's history as a zip
of CSVs, and holding or releasing one chat's automation (ADR 0043) while the
fleet is running. The owner-only routes expose instance
configuration and manage access tokens. YAML edits still enter through files as
well; the watcher loads and validates them and activates only safe changed
scopes (ADR 0009). The CLI uses the API for operational commands while the
instance is running.

The full target beyond P4 is not implemented yet: the complete set of bot
and conversation controls remains planned, while browser sessions and an
operator dashboard are
optional tooling. Hosts can use the current publication API from their own
backends without either (ADR 0046). Scope and bot membership changes and channel
settings currently remain pending until restart; a graph topology change waits
for active sessions in its scope to expire (ADR 0009).

For direct manual edits, Botflota cannot lock out independent editors that
write files themselves. Use one editor per file and atomic file replacement;
separate per-scope include fragments let editors work on different scopes
without writing the same file. Use the API publication path for version-checked
conflict detection. An invalid manual edit is left
on disk; the watcher keeps the last valid active snapshot, logs the cause for
the owner, and never rolls files back. The owner can inspect and correct the
file directly or run `config validate` for detailed diagnostics.
