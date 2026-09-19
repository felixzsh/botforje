# ADR 0035 — Declarative specifications are separate from config loading

Status: Accepted (2026-09). The template ownership is superseded by ADR 0036;
the split of the declarative objects into `spec` stands.

## Context

`config` held both the configuration document (files, includes,
interpolation, origin, validation) and the declarative objects a bot
executes (`Graph`, `Node`, `Action`, `Bot`, `LLM`). The engine imported
`config` only for those objects, so `config` could not use the engine's
template parser to validate references without an import cycle. Template
parsing and the runtime context lived in their own `template` package, used
by both `config` and `engine`.

## Decision

- The declarative objects live in `spec`: scopes, model profiles, actions,
  graphs, bots, and their fields. YAML and JSON tags are metadata for
  decoding; `spec` also owns the `Text` and `Secret` value types with their
  marshaling and schema methods.
- `config` owns documents: `Config` and `Fragment`, loading, includes,
  interpolation, origin tracking, and semantic validation. It imports `spec`
  for the objects it validates.
- `engine` owns execution and the template language: the parser, the runtime
  context, and template validation live in `engine` (`template.go`,
  `context.go`, `template_validate.go`) and are shared through
  `ParseTemplate`, `ParseTemplatePath`, `RenderTemplate`,
  `NewTemplateContext`, and `ValidateTemplates`.
- `engine` no longer imports `config`: it executes `spec.Graph`,
  `spec.Action`, and `spec.Step`. `config` calls `engine.ValidateTemplates`
  from `config/validate.go` and attributes each issue to the file and entity
  that declared it.
- The top-level `template` package is retired.

## Consequences

- The dependency graph is acyclic: `engine → spec`,
  `config → {engine, spec}`, and `fleet`/`cmd` compose all of them.
- Template syntax stays defined in one place, so `config validate` and the
  runtime agree by construction.
- `spec` imports YAML and JSON Schema for decoding and generated
  documentation. That is deliberate: one definition per object, no DTOs and
  no conversions. If codecs ever need to move, they move behind `spec`.
- When behavior moves to the database (ROADMAP Phase 15), the store builds
  `spec` values; no separate "behavior" package is planned.
- Tests that load config fixtures from the engine run as an external test
  package (`package engine_test`), so the test can import `config` without an
  import cycle.
