# ADR 0039 — One owner per instance, one control plane

Status: Accepted (2026-09)

## Context

`fleet run` owns the database, the device store, and one WhatsApp connection
per account. `bot pair` and `bot unpair` write the same stores, and the guard
against running beneath a live fleet was a TCP probe of the API port: with
`port: 0` a running fleet was invisible, and any other process listening on
that port looked like one.

The CLI also mixes two kinds of command: local ones that prepare an instance
(`config`, pairing while stopped) and queries over a running one (`status`,
`chat list`, `message tail`). The dashboard and host integrations have no
local files at all; everything they do must travel through the API.

## Decision

- One process owns an instance. `fleet run` and every local command that
  writes instance state take an exclusive advisory lock on a file under
  `data_dir`; a second owner is refused with an explicit error. The operating
  system drops the lock when the process exits, so a crash never leaves a
  stale guard. Platforms without file locking cannot own an instance and say
  so instead of running unprotected.
- The API is the only control plane of a running instance. The dashboard, host
  integrations, and the CLI's operational commands all use it; while the
  instance is owned there is no fallback from the API to the database. If the
  API does not answer, the command reports that instead of partial data.
- Local, direct access is limited to commands that create or repair an
  instance while no owner runs: `config init|validate|schema`, `fleet run`,
  `bot pair`, and `bot unpair`.
- The CLI is a local client, not a remote one: it may read the config file to
  find the address, the owner token, and the scope names. Callers without
  local files (the dashboard, a host integration) never do.
- SQLite is state, not a message bus: a read sees the latest committed change
  and no consumer is notified. Nothing polls the database for changes, and
  writing behind the API generates no event, log, or audit entry.
- With `port: 0` the instance has no control plane: operational commands say
  so instead of opening the database.

## Consequences

- A second `fleet run`, a pairing beneath a live fleet, or a migration during a
  run fails immediately and truthfully, whatever `port` is set to.
- `bot list` is an instance query: it answers through the API and is available
  only while the instance runs.
- Pairing while the instance runs is an API operation (ADR 0025); pairing
  while it is stopped stays a local one.
- The API may offer capabilities the CLI never mirrors (manual replies,
  takeover): the CLI being a subset of the API is the design, not a gap.
- Auditable actions must enter through the API, where the owner process can
  log and record them.
