# Botflota

Declarative WhatsApp bot framework written in Go. Define bots, conversation
graphs, and actions in YAML; one instance runs a fleet of WhatsApp accounts
through the native Multi-Device protocol, with no browser automation.

## Principles

- Declarative first: behavior lives in YAML; changing a bot never means
  changing the runtime.
- One runtime: a single binary, no mandatory auxiliary processes.
- The graph is the core: models drive and enrich conversations but never
  replace the state machine. A System One model routes and replies are
  prescribed by default; a generative LLM is opt-in. That AI layer is the
  Phase 8 target (see Status below), not the current runtime.
- Config can be authored by hand, by a UI, or AI-assisted; the runtime does
  not change for any of them.

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
- `docs/guide/` — pairing an account and running bots.
- `docs/glossary.md` — terms used across the docs.

## Development

- `PLAN.md` — work toward the MVP and its open decisions.
- `ROADMAP.md` — work planned after the MVP.
- `docs/README.md` — documentation map.
- `CONTRIBUTING.md` — design principles.
- `AGENTS.md` — rules for agent-assisted development.
