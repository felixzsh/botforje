# BotForje — Agent Guide

## What this project is

BotForje is a declarative WhatsApp bot framework written in Go. Bot behavior
is defined in YAML: bots, conversation graphs, actions, and guards. One
instance runs a fleet of WhatsApp accounts through the native Multi-Device
protocol (`go.mau.fi/whatsmeow`) with no browser automation.

This repository is the definitive implementation and will supersede
`botforje-js`. The JS project is reference material only: read it for ideas
and lessons, never copy code and never modify it.

## Sources of truth

- `CONTEXT.md` — identity, decisions, glossary, current state.
- `README.md` — entry point for humans.
- `PLAN.md` — phase-by-phase plan to the MVP.
- `docs/architecture.md` — components, boundaries, message lifecycle.
- `docs/adr/` — why each important decision was made.
- `config.schema.json` (generated from Go types starting in Phase 1) and
  `config.example.yaml` (draft v1) — configuration structure.
- User `config.yml` — behavior of a running bot during the MVP.

When documents disagree, `CONTEXT.md` and the ADRs win over this file.

## Repositories and boundaries

Write only inside this repository.

- `../botforje-js` — legacy reference. Read-only. Never modify, never port
  code or fixtures.
- `../botforje-docs` — snapshot of legacy docs for the JS project. Do not
  update it; all current documentation lives here.

## Commands

TBD. Fill this section with real commands (build, test, validate, run) as
soon as code exists. Until then, do not invent commands.

## Structure

TBD. Expected shape once code exists: `cmd/` for entrypoints and `internal/`
for packages. Replace this guess with the real layout.

## Conventions

- The domain never imports YAML, SQL, whatsmeow, or LLM SDK types.
- Define interfaces at the consumer; keep them small and intentional.
- Keep code, comments, logs, and documentation in English.
- Do not re-litigate settled decisions in code. Propose an ADR change instead.
- A phase from `PLAN.md` is done only when its gate (tests) passes.

## Never

- Modify sibling repositories.
- Add a dependency without explicit approval.
- Mix domain logic with transport, persistence, or CLI concerns.
- Create catch-all packages (`utils`, `helpers`, `common`, `misc`).
- Refactor or fix things that were not requested.
- Delete files without explicit request.
- Optimize before a real bottleneck exists.

## Git

- This repository follows the global rule: Conventional Commits, commit
  settled work, squash related iterations.
- Never run git commands inside sibling repositories.
