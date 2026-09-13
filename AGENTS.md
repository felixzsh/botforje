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
- Read the relevant ADR in `docs/adr/` before touching a settled decision. To
  change one, add or supersede an ADR; do not re-litigate it in code.
- Do not hand-edit generated files; `config.schema.json` is generated from Go
  types starting in Phase 1.

## Dependencies

- Do not add a dependency without explicit approval.

## Conventions

- The domain never imports YAML, SQL, whatsmeow, or LLM SDK types.
- Define interfaces at the consumer; keep them small and intentional.
- Tests are table-driven with `t.Run`; fixtures live in `testdata/`; prefer
  hand-written fakes over a mocking library.
- Keep code, comments, logs, and documentation in English.

## Commands

- `go build ./...`, `go test ./...`, `go vet ./...`, `gofmt -l .` — Go checks.
- `go run ./cmd/botforje validate --config <file>` — validate a config.
- `go run ./cmd/botforje schema -o config.schema.json` — regenerate the
  schema; run it from the repository root so Go comments resolve.

`auth`, `daemon`, `status`, and `reload` arrive with their phases.

## Structure

- `cmd/` — entrypoints.
- `internal/` — packages grouped by concern; adapters never leak into the
  domain (ADR 0004).

## Never

- Mix domain logic with transport, persistence, or CLI concerns.
- Create catch-all packages (`utils`, `helpers`, `common`, `misc`).
- Refactor or fix things that were not requested.
- Delete files without explicit request.
- Optimize before a real bottleneck exists.

## Git

- Conventional Commits; commit settled work and squash related iterations.
- Never run git commands inside sibling repositories.
