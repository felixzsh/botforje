# ADR 0025 — CLI command groups by resource

Status: Accepted (2026-09)

## Context

ADR 0024 defined a flat command set. The API is organized by resource (bots,
sessions, conversations, messages, config), and a flat CLI would mix entity
operations with fleet operations and lose that mapping. A single bot also needs
several operations, which a flat namespace crowds.

## Decision

Commands are grouped by resource, mirroring the API:

- `config init | validate | schema`
- `bot list | pair | unpair`
- `daemon run | status | stop | restart | reload`
- future: `message list | tail`, `session`, `conversation`

A group invoked without a subcommand prints its help. Common flags such as
`--config` stay on each subcommand. `bot list` shows stored pairing, while
`daemon status` will report live fleet health. `daemon reload` reloads config
(ADR 0009) and `daemon restart` is a full restart, so they stay distinct.

This supersedes ADR 0024, which defined the flat set; the verb/noun rule still
holds inside each group.

## Consequences

- CLI and API share resource names.
- Common operations are one level deeper, for example `bot pair`.
- `status` and `reload` live under `daemon`, leaving the root to groups.
