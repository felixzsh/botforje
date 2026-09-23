# ADR 0009 — Config hot reload is automatic and always on

Status: Accepted (2026-09)

## Context

A running fleet should adopt configuration changes without dropping WhatsApp
connections, and it must never lose the last known-good runtime. YAML is the
persisted source of behavior; the active runtime is a separate snapshot.

## Decision

The fleet watches the main config, included files, and include directories.
Every detected change uses one pipeline: load the complete document, resolve
includes and secrets, validate it, compare it with active configuration, check
runtime compatibility, then publish safe changes. Manual edits and future API
publication use this same pipeline; the API does not create a second source of
behavior.

Every reload runs the full pipeline: load, includes, interpolation, and
semantic validation.

- If loading or validation fails, the active runtime stays unchanged and the
  error is logged. No partial document is applied, and the watcher never
  rewrites or rolls back the edited files.
- Scope changes are compared independently. A safe change advances only the
  affected scope's runtime snapshot; bots in other scopes are not rebuilt.
  Bots in the changed scope adopt the new engine and filter between messages,
  without reconnecting their channels or interrupting a turn.
- A change to `shared`, when implemented, affects each scope whose resolved
  model definition changes, not every scope merely because it shares a file.
- Global settings, scope or bot membership, and channel/outbox settings remain
  saved but inactive until restart. Their warning names the affected setting
  or scope; unrelated safe scopes may still activate.
- A graph topology or initial-context shape change waits while a bot has an
  unexpired session. It is retried while the changed YAML remains present.
  Startup also skips a bot whose active session points to a node absent from
  the loaded graph; it never resets that session silently.

The active snapshot can therefore differ from the saved YAML. A process restart
activates restart-required settings; a pending graph edit activates when its
sessions expire or the process restarts with compatible persisted sessions.

## Consequences

- API config publication, when added, writes YAML and invokes this same
  pipeline; drafts remain a client concern.
- A single manual write that changes several scopes is validated as one
  candidate, then its safe scopes activate independently. A sequence of
  separate file writes is not a transaction.
- Direct file edits are outside Botflota's write locks. Use one editor per
  config file, save by replacing the file atomically, and prefer one include
  fragment per scope so edits to different scopes do not contend. The watcher
  validates the final file contents it reads; it cannot merge overlapping
  edits or prevent one editor overwriting another.
- A future API publication validates before writing and leaves files untouched
  on failure. This does not coordinate an API write with a manual editor; the
  API version check protects only publications made through that API.
- Invalid or pending changes remain visible in logs and never replace the
  active snapshot. A later valid edit or an expired session is retried.
- Tests cover include discovery, scope isolation, active-session compatibility,
  validation fallback, and restart-required changes.
