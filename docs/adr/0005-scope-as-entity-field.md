# ADR 0005 — Scope is an explicit entity field

Status: Accepted (2026-09)

## Context

Deriving scope from directory structure, where `actions/client-a/greet.yml`
belongs to scope `client-a`, couples a domain concept to file layout and
complicates identifiers, tooling, and validation.

## Decision

Every entity (action, graph, bot) may declare an optional `scope` field.
Default is `default`. Uniqueness is `(type, scope, name)`. References resolve
only within the same scope. A scope is a generic isolation namespace: a
tenant or client is the motivating example, but it can group anything, such
as teams, environments, or regions. Multi-file organization uses `include`
fragments that may contain any mix of entities; there is no auto-discovery
of hardcoded directories. Global settings are valid only in the main config
file.

## Consequences

- The namespace is visible in the entity itself, independent of file paths.
- Users choose their own file layout without changing semantics.
- Scope isolation is enforced by semantic validation.
- Sessions are isolated per scope, so two scopes never share conversation
  state.
- Paths never define the namespace; configs that relied on directory-derived
  scope are not compatible.
