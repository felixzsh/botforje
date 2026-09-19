# ADR 0009 — Config hot reload is automatic and always on

Status: Accepted (2026-09)

## Context

A running fleet should adopt configuration changes without dropping WhatsApp
sessions, and it must never lose the last known-good configuration. Operators
edit YAML by hand, so reload cannot depend on a command they must remember.
Hot reload is a first-class capability, not an operation to trigger.

Two kinds of change exist: those a running bot can adopt at the next message or
session, and those that change the shape of what is already running.

## Decision

The fleet watches the main config file and every file reached through
`include`, and reloads automatically when one changes. Watching is the
mechanism; the CLI needs no `reload` command.

Every reload runs the full pipeline: load, includes, interpolation, and
semantic validation.

- If validation fails, the previous configuration stays in effect and the
  error is logged. No partial configuration is ever applied.
- A change a bot can adopt safely at the next message or session is applied
  immediately to every bot, without a restart and without touching connected
  WhatsApp sessions.
- A change that cannot be applied to a running bot — a global setting, a
  removed bot, or a removed or structurally changed node or edge an active
  session depends on — is not applied and is never kept silent. The fleet logs
  a warning that a restart is required and names the change.

Global settings and the set of bots require a restart; the running fleet keeps
serving with the old values until then.

## Consequences

- Safe changes reach production as soon as the file is saved; incompatible
  ones are explicit and never silent.
- Editing an included file triggers a reload too, so a multi-file config
  behaves like one document.
- Validation is all-or-nothing, so a broken edit cannot take the fleet down.
- A pending queue, per-graph maintenance mode, and session draining are
  deliberately out of scope for the MVP; a restart covers them.
- Tests must cover validation fallback, live application, and the
  restart-required warning.
