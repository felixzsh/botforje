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

## Configuration and scope operations

YAML remains the source of behavior. The target API lets an authorized scoped
principal read and publish that scope's configuration, including bots,
graphs, actions, and settings, with the same validation as configuration-file
loading. Publication is atomic, rejects stale versions, and reloads valid
changes; changes the running instance cannot safely apply are reported as
restart-required rather than presented as live (PLAN Phase 7; ADRs
0002, 0009, and 0016).

Instance-wide configuration and shared resources are owner operations. Scope
configuration does not become a second source of truth in the API or dashboard:
the API validates and publishes YAML fragments.

### Publication concurrency (planned)

The API writer is not implemented yet. When added, each scope publication will
carry the version the editor read. The server serializes commits to that
scope's fragment and atomically replaces it only if the expected version still
matches. A stale edit gets a conflict and must be reloaded and reconciled by
the caller; the server does not queue or silently retry it. A token does not
limit itself to one in-flight request, so authorization is not a concurrency
control.

Publications to different scope fragments may proceed independently. Their
validated results merge into the active instance snapshot without dropping
another scope's update. Re-publishing identical content is a no-op. The API
uses the same load, validate, compatibility, and scope activation pipeline as
the file watcher; it does not edit a separate in-memory YAML source.

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
conflict detection when it becomes available.
