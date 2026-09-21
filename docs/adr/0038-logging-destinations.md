# ADR 0038 — Logging destinations and command output

Status: Accepted (2026-09)

## Context

Botflota writes one structured log, and every command that opened a connection
wrote it to the terminal at the configured `log_level`. A pairing that imported
a full history window produced one line per chunk — hundreds of them, on a
transfer that can last minutes — and one line per conversation at debug. What
the command had actually stored was buried in the stream, and it was reported
nowhere else.

A one-shot command is not a service. Its contract is the Unix one: the result on
stdout, diagnostics on stderr, and nothing else. A stream of structured records
belongs to a process that runs indefinitely and that a supervisor watches.

## Decision

- A one-shot command answers with a result on stdout and diagnostics on stderr.
  It writes no log stream unless asked to.
- The daemon (`fleet run`) writes the structured log to stderr and nothing else.
  The supervisor owns capture, rotation, retention, and shipping: journald under
  systemd, `docker logs` in a container.
- Botflota writes no log file of its own and never stores logs in the database.
  The database holds state; the log stream holds events about that state. A log
  file would mean reimplementing the rotation and disk-full handling the
  supervisor already provides, and a log table would grow without bound on the
  hot path and vanish exactly when the database is the thing that broke.
- One-shot commands log at `warn` by default, so a contact or a conversation
  that failed to import still says so. `bot pair --verbose` raises the
  application log to `log_level` and the forwarded protocol log to
  `log_level_deps`. Those two settings describe the service, not a command a
  human runs once.
- Levels keep the rule the architecture already states: debug for per-message
  detail, info for lifecycle and state. The history importer therefore logs one
  info line per transfer — when it reports progress 100 or the next sync type
  starts — and the per-chunk line at debug. A transfer still arriving is silent.
- `bot pair` prints what the registration stored: the chats and messages the
  history sync brought, with duplicates and skips, and the contacts the address
  book added. The numbers come from the importer, never from parsing the log.
- The protocol log a channel forwards (whatsmeow) goes into the same structured
  logger on stderr, never the library's stdout helper, so stdout stays the
  command's result and the whole run keeps one format.
- A long bulk operation may print one line per unit of work, because that is the
  result of the operation, not a log stream.

## Consequences

- `bot pair --import-history` prints a few lines instead of hundreds, and the
  numbers it prints are the ones a person asked for.
- A user reading `docker logs` still sees one line per finished transfer and one
  per sent message; the per-chunk detail is one `log_level: debug` away, and
  `bot pair --verbose` shows it for a single pairing.
- `log_level` no longer changes what `bot pair` prints by default, so one config
  gives a quiet command and a detailed service.
- Anything that needs a durable record of what a bot did reads the database, not
  the log; `messages` is that record.
