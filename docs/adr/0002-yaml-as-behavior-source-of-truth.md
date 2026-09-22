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
API writes validated YAML: one include fragment per scope (ADR 0016), written
atomically and applied through the same watcher and validation as a hand edit
(ADR 0009). SQLite keeps runtime state only: chats, sessions, messages,
credentials, and auditing (ADR 0006).

## Consequences

- One loader, one validator, one document: the dashboard, the CLI, and the
  runtime cannot disagree about what a config means.
- Behavior stays versionable and diffable in git; an operator can review a UI
  edit as a file change and roll back by restoring it.
- There is no behavior migration to design: the editor phase (ROADMAP Phase
  15) is the editor, the revision history, and publishing — not a move of
  tables.
- Concurrent edits resolve as file revisions: revalidate and replace against
  an expected version, not through database transactions.
- A fragment the API rewrites keeps its data, not its comments or formatting;
  the hand-written main file is never rewritten by the API.
