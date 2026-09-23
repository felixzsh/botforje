# Documentation

Map of the documentation.

## Project references

- `scope.md` — what Botflota includes.
- `glossary.md` — terms used across the docs.
- `api.md` — API authority, scope coverage, and current versus target behavior.
- `../PLAN.md` — phases to the MVP, progress checkboxes, and open decisions.
- `../ROADMAP.md` — phases after the MVP and the order they arrive in.

## Developer docs

For contributors and agents working on this repository:

- `architecture.md` — components, boundaries, message lifecycle.
- `adr/` — why each decision was made.

## User guide

For people running bots. It lives in `guide/`.

## Where things live

- Identity and principles: `../README.md`.
- Scope: `scope.md`.
- Mechanics: `architecture.md`.
- Rationale: `adr/`.
- Direction: `../PLAN.md` and `../ROADMAP.md`.
- Config shapes: `../config.schema.json` and
  `../config/examples/reference.yml`.
- Working rules for agents: `../AGENTS.md`.

## Rules

- One owner per topic. User pages are self-contained: they explain behavior
  without citing internal design notes.
- Reference is generated from code, not written by hand: config schema,
  CLI help, and API reference.
- Do not invent behavior. Pages are written against frozen shapes and
  working code.
