# ADR 0034 — Package layout, capabilities, and the core

Status: Accepted (2026-09)

## Context

Every package once lived under `internal/`, so a reader could not tell a
product capability from an implementation detail. Later, the declarative
objects lived beside the YAML loader, and the template language lived in the
engine, which made config loading depend on execution.

## Decision

- A product capability gets its own package; a subordinate detail lives inside
  its owner or, as a last resort, in `internal/`.
- The root package `botflota` holds the core contracts: messages, persisted
  records, ports, errors, the structured logger, and the template language. It
  depends on the standard library only.
- `spec` holds the declarative objects (scopes, graphs, actions, bots, model
  profiles) and the `Text` and `Secret` value types, with their YAML/JSON tags.
- `config` owns documents: `Config` and fragments, load, includes,
  interpolation, origin, semantic validation, and template reference checks.
- `engine` owns execution and the runtime context.
- `fleet` owns one actor per bot; its filter and outbox are unexported parts.
- `adapters/<technology>` holds concrete implementations of the core ports,
  grouped by technology: `adapters/sqlite`, `adapters/whatsapp`.
- `cmd/botflota` parses arguments and assembles; it holds no domain logic.
- `internal/` is a last resort, never a default.

Template ownership:

- The template language lives in the core (`template.go`): `Root`,
  `ParseTemplate`, `ParseTemplatePath`, `ValidSegment`, `Template`,
  `References`, and `Render`. `Render` takes a resolver,
  `func(string) (any, error)`, so the language never knows the context type.
- The runtime context stays in `engine` (`context.go`).
- Template reference validation stays in `config`
  (`config/template_validation.go`): it knows the declared graph and node
  variables and the tool inputs.

Visibility is not a support contract. Botflota ships a binary; packages outside
`internal/` are importable because Botflota's own code composes them, not
because they are an SDK, and they carry no compatibility promise beyond the
module's own releases.

## Consequences

- Dependencies point along `config → spec` and `engine → spec`. Neither imports
  the other, and both use the core template language. The core never imports
  adapters.
- Importing an adapter pulls its dependencies (whatsmeow, SQLite, an SDK); the
  core stays free of them.
- `spec` imports YAML and JSON Schema for decoding and generated documentation.
  One definition per object, no DTOs and no conversions.
- Template syntax is defined in one place, so `config validate` and the runtime
  agree by construction.
- The core owns the syntax, `config` owns the provenance of a reference error,
  and the engine owns resolution.
- Future capabilities arrive with their own package (`api`, `control`,
  `dashboard`, `auth`, `webhook`); `internal/` reopens only under the rule
  above.
- When behavior moves to the database (ROADMAP Phase 15), the store builds
  `spec` values; no separate "behavior" package is planned.
