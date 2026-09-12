# ADR 0002 — YAML is the behavior source of truth for the MVP

Status: Accepted (2026-09)

## Context

Behavior can live in files, in a database, or in both. A future dashboard
will need to edit behavior, but building that now would delay the MVP and
would freeze a database schema for entities that are still changing shape.

## Decision

During the MVP, YAML files are the only source of truth for bot behavior
(bots, graphs, actions). SQLite stores runtime state only: the whatsmeow
device store, sessions, conversations, and messages. When the dashboard can
edit behavior, behavior moves to the database and YAML becomes import and
export.

## Consequences

- Behavior is versionable, reviewable, and diffable in git.
- No dual-write problem while the model is still changing.
- Editing behavior requires file access until the dashboard phase.
- The dashboard phase must design the migration (schema plus one-time import
  from YAML) before it starts. That is explicitly out of scope now.
