# Documentation

Map of the documentation.

## Project references

- `scope.md` — what the MVP includes.
- `glossary.md` — terms used across the docs.
- `../CONTEXT.md` — current work state.
- `../PLAN.md` — phases and open decisions.

## Developer docs

For contributors and agents working on this repository:

- `architecture.md` — components, boundaries, message lifecycle.
- `adr/` — why each decision was made.

## User guide

For people running bots. It lives in `guide/` and is written once config v1
is frozen (Phase 1), then finalized when the CLI and API exist.

## Where things live

- Identity and principles: `../README.md`.
- Scope: `scope.md`.
- Mechanics: `architecture.md`.
- Rationale: `adr/`.
- Config shapes: `../config.schema.json` and `../config.example.yaml`.
- Working rules for agents: `../AGENTS.md`.

## Rules

- One owner per topic. User pages link to developer docs for rationale;
  they never repeat them.
- Reference is generated from code, not written by hand: config schema,
  CLI help, and API reference.
- Do not invent behavior. Pages are written against frozen shapes and
  working code.
