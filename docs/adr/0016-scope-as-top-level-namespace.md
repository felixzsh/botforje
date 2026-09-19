# ADR 0016 — Scope is the top-level config namespace

Status: Accepted (2026-09)

## Context

An earlier shape carried an optional `scope` field on each entity, with
identity `(type, scope, name)`, but kept entities in a map keyed by name only.
The scope field did not participate in the key, so two scopes could not both
define `greet`, and a forgotten `scope` silently placed an entity in
`default`. Deriving scope from directories instead couples a domain concept to
file layout.

## Decision

A config file is global settings plus `scopes`, and an optional `shared`
catalog the host provides (ADR 0031). Each scope holds its own `system_one`,
`llms`, `actions`, `graphs`, and `bots`.

```yaml
version: 1
address: "127.0.0.1"
scopes:
  client-a:
    actions: { greet: {...} }
    graphs:  { faq:   {...} }
    bots:    { support-bot: {...} }
```

- Entities do not carry a `scope` field; the namespace is structural and
  identity is `(scope, type, name)` by construction.
- Scope names are free strings. `default` is just a name with no special
  behavior; `shared` is not a scope.
- Actions, graphs, and bots resolve references only inside their own scope.
  Model names (`system_one`, `llm`) resolve in the scope first and then in
  `shared` (ADR 0031); references never cross scopes.
- Scope is never derived from folders. Users pick their own file layout, and
  includes organize files only.
- Global settings live in the main file only.

Includes:

- Any file may `include` others; included files use the same `scopes` shape and
  may not set global settings.
- Several files may contribute to the same scope; a duplicate `(scope, type,
  name)` is an error.
- Include paths resolve relative to the including file.
- A file already loaded is skipped, which allows diamond includes and breaks
  cycles.

## Consequences

- The same names repeat across scopes, which is the point.
- The isolation boundary is structural: a forgotten scope cannot happen.
- Sessions are isolated per scope; two scopes never share conversation state.
- One file per scope, or several fragments per scope, both work and match a
  folder layout without the folder defining the namespace.
