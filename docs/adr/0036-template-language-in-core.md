# ADR 0036 — The template language lives in the core

Status: Accepted (2026-09)

## Context

ADR 0035 moved the template parser, the runtime context, and template
validation into `engine`, so `config` imported `engine` to validate `{{...}}`
references. That made the config loader depend on the execution engine for a
pure string language, and left a package whose only job was parsing embedded
in the executor.

The reference language is shared: `config` parses references to validate them,
`engine` parses and renders them at runtime. The runtime context is different:
it is execution state only the engine builds.

## Decision

- The template language lives in the root package `botflota` (`template.go`):
  `Root`, `ParseTemplate`, `ParseTemplatePath`, `ValidSegment`, `Template`,
  `References`, and `Render`. It imports only the standard library and holds
  no runtime state.
- `Render` takes a resolver, `func(string) (any, error)`, so the language
  never knows the context type.
- The runtime context stays in `engine` (`context.go`): seeding, read and
  write rules, and request results.
- Template reference validation stays in `config`
  (`config/template_validation.go`): it
  knows the declared graph and node variables and the tool inputs.
- `config` no longer imports `engine`.

## Consequences

- The dependency graph is smaller: `config → spec`, `engine → spec`, and both
  use the core language. The `config → engine` edge is gone.
- `Template.Render` is pure: the engine passes its context's resolver and tests
  pass a fake one.
- The core owns the syntax, `config` owns the provenance of a reference error,
  and the engine owns resolution.
- Supersedes the template ownership of ADR 0035; the split of the declarative
  objects into `spec` stands.
