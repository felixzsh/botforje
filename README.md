# Botflota

Declarative WhatsApp bot framework written in Go. Define bots, conversation
graphs, and actions in YAML; one instance runs a fleet of WhatsApp accounts
through the native Multi-Device protocol, with no browser automation.

## Principles

- Declarative first: behavior lives in YAML; changing a bot never means
  changing the runtime.
- One runtime: a single binary, no mandatory auxiliary processes.
- The graph is the core: models drive and enrich conversations but never
  replace the state machine. A System One model routes; replies are prescribed
  by default and a generative LLM is opt-in.
- Config can be authored by hand, by a UI, or AI-assisted; the runtime does
  not change for any of them.

## Status

Botflota is under active development.

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
