# Agent Guide

Operational rules for changing this repository. For what the project is and
which document owns what, read `README.md`.

## Working rules

- Start a new session by reading `docs/glossary.md`; it defines the terms used
  across every document.
- Write only inside this repository. Do not modify sibling repositories.
- Read `PLAN.md` and work on the current phase; check its deliverables in the
  same commit that completes them.
- Read the document that owns a topic before changing it, and update that
  document instead of repeating content elsewhere. Cross-reference files or
  sections.
- ADRs describe only current decisions, with no superseded or retired files
  kept as history. When a decision changes, update or replace its canonical ADR
  and delete the obsolete one after moving any rationale still worth keeping;
  do not re-litigate it in code. Renumbering is avoided, so gaps are fine.
- Do not hand-edit generated files; `config.schema.json` is generated from Go
  types starting in Phase 1.

## Dependencies

- Do not add a dependency without explicit approval.

## Conventions

- The core (`botflota` root package) never imports YAML, SQL, whatsmeow, or
  LLM SDK types.
- Declarative objects live in `spec`, documents are loaded by `config`, and
  `engine` executes them and owns the runtime context. The template language
  lives in the core, and `config` validates references and adds file and entity
  context (ADR 0034).
- Keep interfaces small and intentional; the core owns the ports that adapters
  implement (ADR 0004, ADR 0034).
- Tests are table-driven with `t.Run`; fixtures live in `testdata/`; prefer
  hand-written fakes over a mocking library.
- Keep code, comments, logs, and documentation in English.

## Go

- Errors are values: wrap them with context (`%w`); never ignore one; do not
  `panic` in library code.
- Pass `context.Context` first and honor cancellation and timeouts.
- Every goroutine must have a way to exit; give each channel one clear owner.
- Avoid `init()` and package-level mutable state.
- Do not share maps or slices across goroutines without synchronization.
- Use generics only to remove real duplication.

## Commands

Prefer the Make targets; they wrap the underlying Go commands.

- `make check` — `go vet`, `gofmt` check, and `go test ./...`.
- `make build` — build `bin/botflota`.
- `make schema` — regenerate `config.schema.json` (run from the repository
  root so Go comments resolve).
- `make validate CONFIG=<file>` — validate a config.
- `make help` — list every target.

`config`, `bot`, and `fleet` subcommands arrive with their phases.

## Structure

- Root package `botflota` — core contracts: messages, persisted records,
  ports, errors, the structured logger, and the template language; standard
  library only.
- `spec` — declarative objects: scopes, graphs, actions, bots, and model
  profiles, with their YAML/JSON tags.
- `config` — configuration documents: load, includes, interpolation, origin,
  semantic validation, and template reference checks.
- `engine` — execution of `spec` plus the runtime context; `fleet` — one
  actor per bot.
- `api` — REST handlers and routes; `fleet run` injects the live fleet and
  the stores.
- `adapters/` — concrete implementations of the core ports, grouped by
  technology: `sqlite`, `whatsapp`; later channels and providers join them.
- `cmd/` — entrypoints; argument parsing and assembly only.
- `internal/` — a last resort, never the default: only a shared
  implementation detail that is not a capability and has no natural owner.

A product capability gets its own package; a subordinate detail lives inside
its owner or, as a last resort, in `internal`. Binary versus library does not
decide visibility: packages outside `internal/` are importable because
Botflota's own code composes them, not because they are an SDK, and they
carry no compatibility promise to other projects (ADR 0034). Dependencies
point along `config → spec` and `engine → spec`; neither imports the other,
and both use the core template language. The core never imports adapters
(ADR 0004, ADR 0034). The package map lives in
`docs/architecture.md`; keep it current.

## Never

- Mix domain logic with transport, persistence, or CLI concerns.
- Create catch-all packages (`utils`, `helpers`, `common`, `misc`).
- Refactor or fix things that were not requested.
- Delete files without explicit request.
- Optimize before a real bottleneck exists.

## Git

- Conventional Commits; commit settled work and squash related iterations.
- Never run git commands inside sibling repositories.
