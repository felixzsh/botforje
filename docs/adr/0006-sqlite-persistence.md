# ADR 0006 — SQLite as the only persistence technology in the MVP

Status: Accepted (2026-09)

## Context

The engine must persist whatsmeow credentials, chats, their session runs, and
message history. A single-instance deployment should stay simple to operate;
no external database should be required.

## Decision

SQLite is the project database. One file per instance, located through the
`data_dir` setting (XDG default), with the schema applied on startup. Tables:
chats, sessions, messages, accounts, pairing_status, the whatsmeow device
store, and (with the API) web sessions, the owner token, and access tokens
(ADR 0026). A bot's
identity comes from config; `accounts` records its paired channel identity.
Graph and node contexts are stored as JSON on the session. The domain depends
on repository interfaces, not on SQL.

The driver is pure Go (`modernc.org/sqlite`) so builds stay static; each
connection enables WAL, foreign keys, a busy timeout, and
`synchronous=NORMAL` (validated in the Phase 0 spike). Writers stay bounded
by the per-bot actor model.

## Consequences

- Zero external services; backup is copying files.
- The single-writer model is acceptable for one process with short writes.
- The schema is one list applied on open. Pre-release it is edited in place
  and the development database is recreated (`make dev-reset`); versioned,
  append-only migrations begin at the first release.
- A dashboard or a hosting integration may add tables and an import path; the
  storage engine stays SQLite.
