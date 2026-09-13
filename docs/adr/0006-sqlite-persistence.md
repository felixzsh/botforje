# ADR 0006 — SQLite as the only persistence technology in the MVP

Status: Accepted (2026-09)

## Context

The engine must persist whatsmeow credentials, conversation sessions, and
message history. A single-instance deployment should stay simple to operate;
no external database should be required.

## Decision

SQLite is the project database. One file per instance, located through the
`data_dir` setting (XDG default), with schema migrations on startup. Tables:
bots (identity), senders, whatsmeow device store, sessions, conversations,
messages, and (with the API) web sessions and admin keys. Graph and node
contexts are stored as JSON on the session. The domain depends on repository
interfaces, not on SQL.

The driver is pure Go (`modernc.org/sqlite`) so builds stay static; each
connection enables WAL, foreign keys, a busy timeout, and
`synchronous=NORMAL` (validated in the Phase 0 spike). Writers stay bounded
by the per-bot actor model.

## Consequences

- Zero external services; backup is copying files.
- The single-writer model is acceptable for one process with short writes.
- Migrations must exist from the first table; no manual schema edits.
- Dashboard and SaaS phases may add tables and an import path; the storage
  engine stays SQLite.
