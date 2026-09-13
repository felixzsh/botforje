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
- Keep code, comments, logs, and documentation in English.

## Commands

TBD. Fill with the real commands (build, test, validate, run) once code
exists. Until then, do not invent commands.

## Structure

TBD. Expected shape: `cmd/` for entrypoints and `internal/` for packages.
Replace this guess with the real layout.

## Never

- Mix domain logic with transport, persistence, or CLI concerns.
- Create catch-all packages (`utils`, `helpers`, `common`, `misc`).
- Refactor or fix things that were not requested.
- Delete files without explicit request.
- Optimize before a real bottleneck exists.

## Git

- Conventional Commits; commit settled work and squash related iterations.
- Never run git commands inside sibling repositories.
