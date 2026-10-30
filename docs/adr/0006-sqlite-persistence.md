# ADR 0006 — SQLite as the only persistence technology in the MVP

Status: Accepted (2026-09)

## Context

The engine must persist whatsmeow credentials, chats, their session runs, and
message history. A single-instance deployment should stay simple to operate;
no external database should be required.

## Decision

SQLite is the project database. One file per instance, located through the
`data_dir` setting (XDG default), with the schema applied on startup. Tables:
chats, sessions, messages, accounts, the whatsmeow device
store, and (with the API) web sessions, the owner token, and access tokens
(ADR 0026). A bot's
identity comes from config; `accounts` records its paired channel identity.
Graph and node contexts are stored as JSON on the session. The domain depends
on repository interfaces, not on SQL.

The driver is pure Go (`modernc.org/sqlite`) so builds stay static; each
connection enables WAL, foreign keys, a 30-second busy timeout, and
`synchronous=NORMAL` (validated in the Phase 0 spike). Each bot is one writer
actor, but a fleet has many, so the process caps its pool at 8 connections:
SQLite admits one writer at a time, and extra connections add page caches and
lock contention without adding write throughput. The busy timeout decides
whether a queued turn waits or fails; a failed turn is reported and stays
replayable, never silently dropped. Up to eight connections stay idle for
reuse, so a burst does not reopen and reconfigure them. Both bounds are what let
a fleet of hundreds of bots stay inside its memory and latency budgets
(`docs/channels/load-testing-plan.md`, section 6).

The timeout is a per-statement lock wait, not an operation deadline. SQLite's
busy handler cannot observe Go's context, so a cancelled request still waits out
the remaining timeout; callers that need prompt cancellation cannot rely on it.

## Consequences

- Zero external services; backup is copying files.
- The single-writer model is acceptable for one process with short writes:
  `fleet run` and the offline commands that write take an exclusive lock on
  `data_dir`, so one owner holds it across processes and not only between
  actors (ADR 0039).
- The schema is one list applied on open. Pre-release it is edited in place
  and the development database is recreated (`make dev-reset`); versioned,
  append-only migrations begin at the first release.
- A dashboard or a hosting integration may add tables and an import path; the
  storage engine stays SQLite.
