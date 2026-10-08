# Botflota

Declarative WhatsApp bot framework written in Go. Define bots, conversation
graphs, and actions in YAML; one instance runs a fleet of WhatsApp accounts
across scopes through the native Multi-Device protocol, with no browser
automation.

## Principles

- Declarative first: behavior lives in YAML; changing a bot never means
  changing the runtime.
- One runtime: a single Go process runs the fleet across scopes, with no
  browser process per bot or mandatory auxiliary process (ADR 0001).
- Per-bot operations: pairing or unpairing one bot does not stop its peers;
  process lifecycle belongs to the host's supervisor (ADR 0039).
- The graph is the core: models drive and enrich conversations but never
  replace the state machine. A System One model routes and replies are
  prescribed by default; a generative LLM is opt-in. That AI layer is the
  Phase 8 target (see Status below), not the current runtime.
- Config can be authored by hand, by a UI, or AI-assisted; the runtime does
  not change for any of them.
- Independent hosts own their applications and identity models; Botflota owns
  execution and its generic control plane. A host does not need a Botflota
  dashboard or browser handoff (`docs/integration.md`; ADR 0046).

## Status

Botflota is under active development.

What runs today:

- YAML config with strict loading, semantic validation, and a generated
  schema; `config init|validate|schema`.
- SQLite persistence where each message turn commits atomically and replies
  wait in a durable outbox (ADR 0040).
- Deterministic graph routing (triggers, default edge, fallback), prescribed
  replies, actions, guards, templates, and requests.
- The WhatsApp channel through whatsmeow: pairing, pacing, history import,
  and a REST control plane with owner and scoped tokens.
- Config hot reload, an instance lock, and the CLI as an API client.

What is not implemented yet, and what the architecture document describes as
the target design: System One confidence routing, the tool pipeline, and
opt-in LLM realization — all of them Phase 8 in `PLAN.md`.

## Documentation

- `config/examples/reference.yml` — reference configuration.
- `docs/guide/` — pairing an account, running bots, and running as a systemd
  service.
- `docs/glossary.md` — terms used across the docs.
- `docs/integration.md` — runtime versus host responsibilities and integration.

## Development

- `PLAN.md` — work toward the MVP and its open decisions.
- `ROADMAP.md` — work planned after the MVP.
- `docs/README.md` — documentation map.
- `CONTRIBUTING.md` — design principles.
- `AGENTS.md` — rules for agent-assisted development.

## License

Copyright (C) 2026 Botflota contributors.

Botflota is free software: you can redistribute it and/or modify it under the
terms of the GNU Affero General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later version
(`AGPL-3.0-or-later`).

Botflota is distributed without any warranty, including the implied warranties
of merchantability or fitness for a particular purpose. See [LICENSE](LICENSE)
for the full terms. Third-party dependencies retain their own licenses and notices.
