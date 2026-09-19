# ADR 0025 — CLI command groups, vocabulary, and output

Status: Accepted (2026-09)

## Context

Commands must stay predictable as the CLI grows, mirror the API's resource
organization, and guide users without guessing their scope or state. An
earlier flat command set mixed entity operations with fleet operations and lost
that mapping.

## Decision

Commands are grouped by resource, mirroring the API:

- `config init | validate | schema`
- `bot list | pair | unpair`
- `fleet run | status`
- future: `message list | tail`, `session`, `chat`

A group invoked without a subcommand prints its help. Common flags such as
`--config` stay on each local subcommand. `fleet run` hosts the fleet and the
REST API; operational queries such as `fleet status` and `message list|tail`
are API clients.

Process lifecycle belongs to the supervisor, not to Botflota. The CLI has no
`stop` or `restart`: systemd, Docker, or another supervisor starts, stops, and
restarts the process. Botflota handles SIGINT and SIGTERM with an ordered
shutdown that drains the outbox. Config reload is automatic (ADR 0009), so
there is no `reload` command either.

Vocabulary:

- Action commands use a verb (`init`, `validate`, `pair`, `unpair`).
- Query commands use a noun (`schema`, `status`, `messages`); the long-running
  process command is `fleet`.
- Device pairing is `pair`; removing the local pairing is `unpair`. `link` and
  `unlink` are rejected because `unpair` is local and does not unlink the
  companion in WhatsApp.
- `auth` is reserved for authentication (admin key, sessions) and never names
  channel pairing.

Output guidance:

- Any output a user sees names at most the command path (group and subcommand),
  never an invocation with flags, options, or entity arguments.
- Only command help and usage document syntax, and each command guides the user
  when called incompletely or ambiguously.

## Consequences

- CLI and API share resource names, and the verb/noun rule holds inside each
  group.
- Common operations are one level deeper, for example `bot pair`.
- `status` lives under `fleet`, leaving the root to groups.
- Operational CLI commands read the same API used by the dashboard instead of
  duplicating live-state access.
- Service lifecycle uses the supervisor's native commands; Botflota never
  detects or invokes systemd or a container runtime.
- Messages stay valid under scope or entity ambiguity: they point to `bot pair`,
  not to a specific invocation.
- The process group is `fleet`, matching the glossary.
