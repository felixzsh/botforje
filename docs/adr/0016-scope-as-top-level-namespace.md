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
address: "127.0.0.1"
scopes:
  support:
    display_name: "Support"
    actions: { greet: {...} }
    graphs:  { faq:   {...} }
    bots:    { support-bot: {...} }
```

- Entity map keys are their stable identifiers and remain the keys used by
  references, storage, and authorization. Scope names are free strings.
- `display_name` is an optional, editable label on scopes and their entities.
  Interfaces show it when non-empty and otherwise show the map key. Changing the
  label never changes the key, references, or persisted runtime identity.
- A UI-created child entity uses its initial submitted name as its map key.
  Later UI renames change `display_name`, not the key. A future owner scope
  provisioning API and its naming rules are TBD under ADR 0046 and
  `docs/access-policy.md`; no generated-key exception is an implemented contract.
- Effective display labels are unique within their UI namespace: scopes across
  the instance, each entity category within a scope, nodes within a graph, and
  model catalogs within their owner. Different categories or scopes may reuse
  a label. Validation compares `display_name` when non-empty and otherwise the
  key; creation and rename must enforce this server-side.
- Renaming a map key changes identity. A manual edit that changes a key is not
  inferred as a rename; preserving identity across a key change needs an
  explicit operation with its own migration semantics.
- `default` is just a scope name with no special behavior; `shared` is not a
  scope.
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
  key)` is an error. Distinct entities in one scope may therefore have
  different source files; the loader records the defining file for each one.
- A scope's non-empty `display_name` may be declared in one contributing file;
  a conflicting value in another file is an error.
- Include paths resolve relative to the including file.
- A file already loaded is skipped, which allows diamond includes and breaks
  cycles.

## Consequences

- The same names repeat across scopes, which is the point.
- The isolation boundary is structural: a forgotten scope cannot happen.
- Sessions are isolated per scope; two scopes never share conversation state.
- One file per scope, or several fragments per scope, both work and match a
  folder layout without the folder defining the namespace.
- A scope is the logical unit exposed by the API. Its storage layout stays
  internal; API publication uses entity origins for edits and a managed
  fragment for new definitions (ADR 0002).
