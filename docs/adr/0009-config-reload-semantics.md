# ADR 0009 — Config reload semantics

Status: Accepted (2026-09)

## Context

A running daemon should adopt config changes without dropping WhatsApp
sessions, and it should never lose the last known-good configuration. Some
changes are safe at any moment; others would break sessions that are in the
middle of a conversation.

## Decision

The daemon watches config files and reloads on SIGHUP. Every reload runs the
full validation pipeline. If validation fails, the previous valid config
stays in effect and the error is logged.

Changes are classified:

- Safe: new or updated actions, graphs, bots, nodes, and edges that no active
  session depends on. Applied immediately.
- Incompatible: removing or structurally changing nodes or edges used by
  active sessions. They enter a pending queue.

A pending queue is applied through maintenance mode for the affected graph:

1. Stop accepting new sessions for the graph.
2. Let active sessions finish or expire.
3. Apply the queued changes.
4. Reactivate the graph.

Reload never touches connected WhatsApp sessions and never applies global
settings; those require a restart.

## Consequences

- Config can evolve in production without downtime for safe changes.
- Incompatible changes are explicit, queued, and applied when safe.
- The pending queue and maintenance mode add state to persist and expose
  through the API (`status`, `reload`).
- Tests must cover validation fallback and both change classes.
