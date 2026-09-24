# ADR 0002 — YAML is the behavior source of truth

Status: Accepted (2026-09)

## Context

Behavior can live in files, in a database, or in both. The dashboard will one
day edit bots, graphs, and actions for callers with no file access, and an
earlier draft of the roadmap moved those entities into SQLite for that editor.
Moving them would freeze a schema for entities still changing shape, split one
document into two sources, and give the API a second write path beside the
loader that validates includes, references, and templates.

## Decision

YAML files stay the source of truth for bot behavior — for the MVP and after
the dashboard exists. The dashboard edits behavior through the API, and the
API writes validated YAML and applies it through the same loader and activation
pipeline as a hand edit (ADR 0009). A scope is one logical document, though
its entities may live in several included files (ADR 0016). The API follows
the loader's entity origins to update or remove definitions in their source
files. This includes the main config file when it contains the addressed
scope. New definitions go to a deterministic API-managed scope fragment; the
API may update the main file's include list when needed to make that fragment
reachable. These edits are limited to the requested scope and necessary
includes; global settings and unrelated scopes are preserved. A publication
that touches several files is all-or-nothing; failure leaves their previous
contents intact. SQLite keeps runtime state only: chats, sessions, messages,
credentials, and auditing (ADR 0006).

## Consequences

- One loader, one validator, one document: the dashboard, the CLI, and the
  runtime cannot disagree about what a config means.
- Behavior stays versionable and diffable in git; an operator can review a UI
  edit as a file change and roll back by restoring it.
- There is no behavior migration to design: the editor phase (ROADMAP Phase
  15) is the editor and publishing — not a move of
  tables.
- Concurrent API edits use a logical scope version and serialize overlapping
  file commits. A change to a different scope in a shared file is preserved;
  a stale edit to the same scope conflicts instead of overwriting newer data.
- API edits preserve unrelated definitions and files, but may rewrite the
  main file when an authorized target scope is defined there or an include
  must be registered. Formatting and comments in a rewritten source file are
  not part of the config contract. Using includes for scope organization is a
  user choice and a maintainability recommendation, not an API restriction.
- Entity map keys remain stable identifiers; optional `display_name` values
  are presentation metadata. API/dashboard edits may change the label without
  changing references or runtime identity. Missing labels remain absent from
  YAML; consumers display the key as the fallback.
