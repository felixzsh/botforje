# Agent Guide

Operational rules for changing this repository. For what the project is and
which document owns what, read `README.md`.

## Working rules

- Start a new session by reading `docs/glossary.md`; it defines the terms used
  across every document.
- Botflota is a public framework for any deployment: never name external
  products or import their identity, offering, or branding concepts into code,
  comments, commits, or docs. Use `docs/glossary.md` — the integrator is
  `host`, isolation is `scope`, the credential is a `scoped token` (see
  `ROADMAP.md`, "Outside the boundary").
- Keep runtime and host application responsibilities separate (ADR 0046).
  `docs/integration.md` owns the integration flow; unresolved resource policy
  stays TBD in `docs/access-policy.md`, never an implicit coding requirement.
- Write only inside this repository. Do not modify sibling repositories.
- Read `PLAN.md` and work on the current phase; check its deliverables in the
  same commit that completes them.
- Read the document that owns a topic before changing it, and update that
  document instead of repeating content elsewhere. Cross-reference files or
  sections.
- For a change that needs a strong decision — one that will shape, restrict,
  or constrain the project from there on — read `docs/adr/` first; if no
  ADR covers it, stop and invite the decision before implementing.
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
- Keep a file readable in one sitting (at most 500 lines — an estimate,
  never a target to split for its own sake). When one grows well past
  that, split it by type, port, or responsibility; never cut code in
  half. Test files split the same way, mirroring their source files.

## Comments

- Make the code speak for itself first: name files, types, functions, and
  variables so well that a comment is rarely needed. Only comment what the
  code cannot say: a why, a non-obvious contract or gotcha, or where a
  settled decision lives (an ADR pointer once per type, never per method).
- Never restate the code, narrate edits, or repeat what `docs/glossary.md`,
  an ADR, or the contract in `ports.go` and `records.go` already states.
- Exception: `spec` and `config` doc comments feed `config.schema.json`;
  they document the config for its author and stay.

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
