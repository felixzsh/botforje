# ADR 0016 — Scope is the top-level config namespace

Status: Accepted (2026-09)

## Context

ADR 0005 introduced `scope` as a field on each entity, with identity
`(type, scope, name)`. The config shape, however, was a map keyed by name
only: `actions: { greet: ... }`. The scope field did not participate in the
key, so two scopes could not both define `greet`, and a scope could not
define two entities with the same name. That contradicts ADR 0005 and the
motivating case (several clients, each with its own `pricing`, `greet`, and
so on). It also let a forgotten `scope` silently place an entity in
`default`.

## Decision

A config file is global settings plus a `scopes` map. Each scope holds its
own `deciders`, `llms`, `actions`, `graphs`, and `bots` (ADR 0017, ADR 0031).
Scope names are free strings; `default` is just a name, with no special
behavior.

```yaml
version: 1
address: "127.0.0.1"
scopes:
  client-a:
    actions: { greet: {...} }
    graphs:  { faq:   {...} }
    bots:    { support-bot: {...} }
```

- Entities no longer carry a `scope` field; the namespace is structural.
- Identity is `(scope, type, name)` by construction.
- References resolve only inside the same scope.
- Global settings remain only in the main file.

Includes:

- Any file may `include` others; included files use the same `scopes` shape
  and may not set global settings.
- Several files may contribute to the same scope, under the same rules as a
  single file (duplicate `(scope, type, name)` is an error).
- Include paths resolve relative to the including file.
- A file already loaded is skipped, which both allows diamond includes and
  breaks cycles.

This supersedes the entity-field placement of ADR 0005. The rest of ADR 0005
stands: scope is not derived from folders, includes are for organization, and
global settings live in the main file.

## Consequences

- The same names can repeat across scopes, which is the point.
- The isolation boundary is structural: a forgotten scope cannot happen.
- One file per scope, or several fragments per scope, both work and match a
  folder layout without the folder defining the namespace.
- The config shape changes; `config.example.yaml`, the loader, validation,
  and fixtures are updated with it.
- Deleting or renaming a scope is a whole-block change, which is easy to
  review.
