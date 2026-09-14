# ADR 0018 — Template resolution semantics

Status: Accepted (2026-09)

## Context

Messages and requests reference runtime conversation values as
`{{path.to.value}}` (`docs/architecture.md`). The roots are fixed, but
the edges were not: what happens when a path is missing, how a literal `{{`
is written, and which roots may be written.

Rendering an empty string for a missing path hides a typo or an unset
variable and can send a half-formed message; keeping the literal `{{...}}`
leaks an internal path to the user. Both make failures invisible where the
conversation matters most.

## Decision

A reference is `{{`, a dot-separated path, and `}}`; whitespace just inside
the braces is ignored. The first segment selects a root:

- `graph`, `node` — read-write variables (ADR 0008); `node` resets on every
  node entry.
- `sender`, `message`, `bot`, `input` — read-only for the duration of a
  message; the engine seeds them.
- Request results live under `node.<request>`, addressed as
  `resp.body.*`, `resp.headers.*`, `status`, and `duration` (milliseconds).

Rules:

- A missing path is an error that names the path; rendering never silently
  drops text.
- A whole map or slice has no text form and is an error; slice elements are
  addressed with a numeric segment.
- A literal `{{` is written `\{{`; `\\` renders one backslash.
- An unknown root fails when the reference is parsed; a missing path and a
  non-scalar value fail when it is rendered.

## Consequences

- Failures surface at the step that introduced them, not in the user's chat.
- A step renders only against a context that declares its variables;
  `initial_context` and tool `inputs` are the source of those declarations.
- Config-time validation of a reference against declared variables is still
  open and lands with the engine (Phase 4).
- Escaping is one small documented rule; no alternate template syntax
  appears.
