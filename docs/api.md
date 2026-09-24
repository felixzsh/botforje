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

Planned scope provisioning is owner-only and creates a unique generated scope
key; it does not grant that scope to any token. Owner credential management
grants access separately. Limits across a group of scopes are also owner
policy, not a property of a scoped token or a scoped-editable config document.
The group policy's persistence and API shape need an ADR before implementation
(`docs/config-fromapi-plan.md`); scoped publication must not bypass its limits.

## Configuration and scope operations

YAML remains the source of behavior. The target API lets an authorized scoped
principal read and publish that scope's configuration, including bots,
graphs, actions, and settings, with the same validation as configuration-file
loading. Publication is atomic, rejects stale versions, and reloads valid
changes; changes the running instance cannot safely apply are reported as
restart-required rather than presented as live (PLAN Phase 7; ADRs
0002, 0009, and 0016).

Configuration entities keep their YAML map keys as identifiers. Their optional
`display_name` is presentation metadata, not a reference or identity. A UI
shows the key when the field is absent or empty; editing that label writes the
field without renaming the key. API read models must distinguish the stored
optional field from its presentation fallback rather than writing the fallback
into YAML. Changing a key is not a display rename and must not be inferred as
one during reload. Future scope provisioning generates a unique scope key;
manual YAML authors choose their own. For UI-created child entities, the
submitted initial name is the YAML key; later renames update only
`display_name`. Scope creation is the exception: its owner API generates the
key and can store the requested initial label as `display_name`.

The server validates label uniqueness independently of the UI: effective
labels must be unique among scopes instance-wide, among members of each entity
category in a scope, among nodes in a graph, and within each model catalog.
Equal labels across different categories or scopes are allowed. The precise
case/whitespace comparison rule remains to be settled before implementing the
API.

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

Each publication carries the logical scope version the editor read. At commit,
the server reloads the latest include tree, applies the requested scope change
to its source files, validates the complete candidate, and publishes every
affected file as one recoverable all-or-nothing change. It serializes writes to
overlapping files. If another API publication changed only a different scope
in a shared file, the server rebases the requested scope onto that latest
file content so neither update is lost. If the target scope itself changed
since it was read, the caller gets a conflict and must reload and reconcile;
the server does not queue or silently retry stale edits. A token does not limit
itself to one in-flight request, so authorization is not a concurrency
control.

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
replies before the account is removed. The target adds the remaining
scope operations, including bot controls and complete configuration editing,
without stopping peer bots. Ordinary errors in one bot must remain local to
that bot.

The `fleet run` process remains under the host supervisor. A future
owner-only dashboard/API action may request a process restart through that
supervisor; scoped authority cannot control process lifecycle. A scope
operation must not stop the whole fleet. Instance-wide changes that need a
process restart must say so explicitly.

## Current implementation and target

Today, the API has owner and scoped bearer tokens, checks scope access on
scope-addressed routes, reads scope configuration, and supports pairing,
pairing cancellation, and unpairing while the fleet is running. The owner-only
routes expose instance configuration and manage access tokens. It does not yet
write configuration. YAML edits currently enter through files; the watcher
loads and validates them and activates only safe changed scopes (ADR 0009).
The CLI uses the API for operational commands while the instance is running.

The full target is not implemented yet: browser sessions, scope configuration
publication, the complete set of bot and conversation controls, and the
embedded dashboard remain planned. Scope publication is a Phase 7 deliverable;
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
